<script lang="ts">
  import { onMount } from 'svelte';
  import { identityAPI, type IdentityLink, type RecentProof } from '../api/identity';
  import { externalPopup } from '../helper/auth-popup';
  import { isWebAuthnSupported, startAuthentication } from '../helper/webauthn';
  import { storeAuth } from '../store/auth.svelte';
  let { purpose, onproof, oncancel }: { purpose: string; onproof: (proof: string) => Promise<void>; oncancel: () => void } = $props();
  let password = $state(''); let busy = $state(false); let error = $state(''); let links = $state<IdentityLink[]>([]);
  let challenge = $state(''); let code = $state(''); const controller = new AbortController();
  onMount(() => { identityAPI.get<IdentityLink[]>('identities').then(r => links = r.data || []).catch(() => {}); return () => { controller.abort(); password = code = ''; }; });
  async function run(action: () => Promise<RecentProof | { mfa_required: true; challenge: string }>, popupFlow = false) {
    if (busy) return; busy = true; error = '';
    try { const result = await action(); if ('proof' in result && result.proof) await onproof(result.proof); else if ('mfa_required' in result) challenge = result.challenge; else throw new Error('Invalid proof'); }
    catch (e) { if (!controller.signal.aborted) error = popupFlow && e instanceof Error && !('isAxiosError' in e) ? e.message : 'Verification or the requested change failed. Check your credentials and retry with a fresh verification.'; }
    finally { password = code = ''; busy = false; }
  }
</script>
<section class="settings-section settings-form" aria-label="Verify your identity">
  <h3 class="font-semibold">Verify your identity</h3><p class="settings-note">Use a current sign-in method to authorize this change. Verification expires after five minutes.</p>
  {#if challenge}
    <form class="space-y-4" onsubmit={e => { e.preventDefault(); void run(async () => (await identityAPI.post('external/reauth/finish', { challenge, code })).data); }}>
      <label>Authenticator or backup code<input bind:value={code} required autocomplete="one-time-code" /></label><button class="settings-primary" disabled={busy}>Verify code</button>
    </form>
  {:else}
    <form class="space-y-4 max-w-md" onsubmit={e => { e.preventDefault(); void run(async () => { const { data } = await identityAPI.post('reauth/begin', { purpose, method: 'password' }); return (await identityAPI.post('reauth/finish', { challenge: data.challenge, password })).data; }); }}>
      <label>Current password<input type="password" bind:value={password} required autocomplete="current-password" /></label>
      <button class="settings-primary" disabled={busy}>Verify with password</button>
    </form>
    <div class="flex flex-wrap gap-2">
      {#if storeAuth.passkeys}<button class="settings-button" disabled={busy || !isWebAuthnSupported()} onclick={() => run(async () => { const { data } = await identityAPI.post('reauth/begin', { purpose, method: 'passkey' }); const credential = await startAuthentication(data.publicKey, controller.signal); if (!credential) throw new Error('Cancelled'); return (await identityAPI.post('reauth/finish', { challenge: data.challenge, credential })).data; })}>Verify with passkey</button>{/if}
      {#each [...new Set(links.map(link => link.provider_id))] as provider}<button class="settings-button" disabled={busy} onclick={() => { const popup = externalPopup(provider, { purpose: 'reauth', reauth_purpose: purpose }, controller.signal); void run(async () => (await popup).result as RecentProof, true); }}>Verify with linked provider {provider}</button>{/each}
    </div>
  {/if}
  {#if error}<p role="alert" class="settings-error">{error}</p>{/if}
  <button class="settings-button" disabled={busy} onclick={oncancel}>Cancel verification</button>
</section>
