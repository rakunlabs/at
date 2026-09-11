<script lang="ts">
  import { onMount } from 'svelte';
  import QRCode from 'qrcode';
  import RecentAuth from '../lib/components/RecentAuth.svelte';
  import { identityAPI, type IdentityLink } from '../lib/api/identity';
  import { listAuthPasskeys, passwordPolicyError, type AuthPasskey } from '../lib/api/auth';
  import { externalPopup } from '../lib/helper/auth-popup';
  import { isWebAuthnSupported, startRegistration } from '../lib/helper/webauthn';
  import { storeAuth, returnToLogin, securityCodes } from '../lib/store/auth.svelte';
  import { storeNavbar } from '../lib/store/store.svelte';
  storeNavbar.title = 'Account security';
  let status = $state<{enabled: boolean; backup_codes_remaining: number} | null>(null);
  let keys = $state<AuthPasskey[]>([]); let links = $state<IdentityLink[]>([]); let providers = $state<{id: string; label: string}[]>([]);
  let error = $state(''); let busy = $state(false); let loaded = $state(false); let notice = $state('');
  let purpose = $state(''); let action: ((proof: string) => Promise<void>) | undefined;
  let password = $state(''); let confirm = $state(''); let keyName = $state(''); let code = $state('');
  let enrollment = $state<{enrollment: string; secret: string; otpauth_uri: string} | null>(null); let qr = $state('');
  let linkProof = $state(''); let provider = $state('');
  let factorProof = $state(''); let factorPath = $state('');
  const controller = new AbortController();
  async function load() { busy = true; error = ''; try {
    const [s, l, p, k] = await Promise.all([identityAPI.get('totp/status'), identityAPI.get('identities'), identityAPI.get('login-providers'), storeAuth.passkeys ? listAuthPasskeys() : Promise.resolve({items: []})]);
    status = s.data; links = l.data || []; providers = p.data || []; keys = k.items || []; loaded = true;
  } catch { error = 'Cannot load account security. Check your connection and reload.'; } finally { busy = false; } }
  onMount(() => { void load(); return () => { controller.abort(); password = confirm = code = linkProof = factorProof = qr = ''; enrollment = null; }; });
  function authorize(p: string, fn: (proof: string) => Promise<void>) { purpose = p; action = fn; error = notice = ''; }
  async function authorized(proof: string) { await action?.(proof); purpose = ''; action = undefined; }
  function holdBackup(codes: string[]) { securityCodes.values = codes; enrollment = null; qr = code = ''; storeAuth.securityHold = true; }
  async function mutateFactor(path: string, proof: string) {
    storeAuth.securityHold = true;
    try { const { data } = await identityAPI.post(path, { proof, code }); code = ''; if (data?.backup_codes) holdBackup(data.backup_codes); else returnToLogin(); }
    catch (e) { storeAuth.securityHold = false; throw e; }
  }
  async function confirmEnrollment(e: SubmitEvent) { e.preventDefault(); if (busy) return; busy = true; error = ''; storeAuth.securityHold = true;
    try { const { data } = await identityAPI.post('totp/confirm', { enrollment: enrollment!.enrollment, code }); holdBackup(data.backup_codes); }
    catch { storeAuth.securityHold = false; error = 'Could not activate the authenticator. Enter a fresh code, or start enrollment again if it expired.'; }
    finally { busy = false; code = ''; }
  }
  async function finishFactor(e: SubmitEvent) {
    e.preventDefault(); if (busy) return; busy = true; error = '';
    try { await mutateFactor(factorPath, factorProof); }
    catch { error = 'The authenticator change failed. Verify again, then use a fresh authenticator code or unused backup code.'; }
    finally { factorProof = code = ''; busy = false; }
  }
  function link() {
    const popup = externalPopup(provider, { purpose: 'link', recent_proof: linkProof }, controller.signal); linkProof = ''; busy = true;
    popup.then(async () => { notice = 'Account linked.'; await load(); }).catch((e) => { error = e instanceof Error ? e.message + ' Verify again to retry linking.' : 'Linking failed. Verify again and retry.'; }).finally(() => busy = false);
  }
</script>
<div class="settings-page settings-form">
  <header><h1 class="text-2xl font-semibold">Account security</h1><p class="settings-note mt-2">Manage sign-in methods for {storeAuth.identity?.name || 'your account'}.</p></header>
    {#if error}<p role="alert" class="settings-error">{error}</p>{/if}{#if notice}<p role="status" class="settings-note">{notice}</p>{/if}
    {#if !loaded}<button class="settings-button" onclick={load} disabled={busy}>{busy ? 'Loading security…' : 'Reload security'}</button>{:else}
    {#if purpose}<RecentAuth {purpose} onproof={authorized} oncancel={() => { purpose = ''; action = undefined; }} />{/if}
    <section class="settings-section"><h2 class="text-lg font-semibold">Authenticator</h2>
      <p class="settings-note">{status?.enabled ? `Enabled · ${status.backup_codes_remaining} backup codes remaining` : 'Add a second step to password, passkey and identity-provider sign-in.'}</p>
      {#if enrollment}
        <p class="settings-note">Scan this QR code with your authenticator. It is generated locally in your browser. Enrollment expires after five minutes.</p>
        {#if qr}<img src={qr} alt="Authenticator enrollment QR code" width="224" height="224" class="rounded-md" />{/if}
        <label>Manual setup key<input readonly value={enrollment.secret} class="font-mono" onclick={e => e.currentTarget.select()} /></label>
        <p class="settings-note">Time-based (TOTP), 6 digits, SHA-1, 30-second period.</p>
        <form class="space-y-4 max-w-md" onsubmit={confirmEnrollment}><label>Six-digit code<input bind:value={code} inputmode="numeric" pattern="[0-9]{6}" required autocomplete="one-time-code" /></label><button class="settings-primary" disabled={busy}>Activate authenticator</button> <button type="button" class="settings-button" disabled={busy} onclick={() => { enrollment = null; qr = code = ''; }}>Cancel enrollment</button></form>
      {:else if status?.enabled}
        <p class="settings-note">Regenerating codes or removing the authenticator ends all sessions.</p>
        {#if factorProof}<form class="space-y-4 max-w-md" onsubmit={finishFactor}><label>Fresh authenticator or unused backup code<input bind:value={code} required autocomplete="one-time-code" /></label><p class="settings-note">If you just used an authenticator code during verification, wait for the next code.</p><button class="settings-primary" disabled={busy}>{factorPath === 'totp/remove' ? 'Remove authenticator and sign out' : 'Generate new backup codes'}</button><button type="button" class="settings-button" disabled={busy} onclick={() => { factorProof = code = ''; }}>Cancel</button></form>
        {:else}<div class="flex flex-wrap gap-2"><button class="settings-button" disabled={!!purpose || busy} onclick={() => authorize('totp.backup-codes', async proof => { factorProof = proof; factorPath = 'totp/backup-codes/regenerate'; code = ''; })}>Regenerate backup codes</button><button class="settings-button" disabled={!!purpose || busy} onclick={() => authorize('totp.remove', async proof => { factorProof = proof; factorPath = 'totp/remove'; code = ''; })}>Remove authenticator</button></div>{/if}
      {:else}<button class="settings-primary" disabled={!!purpose || busy} onclick={() => authorize('totp.enroll', async proof => { const { data } = await identityAPI.post('totp/enroll', { proof }); enrollment = data; qr = await QRCode.toDataURL(data.otpauth_uri, { width: 224, margin: 4 }); })}>Set up authenticator</button>{/if}
    </section>
    <section class="settings-section"><h2 class="text-lg font-semibold">Password</h2><p class="settings-note">Set or change a local password using any current sign-in method. This ends all sessions.</p>
      <form class="space-y-4 max-w-md" onsubmit={e => { e.preventDefault(); error = passwordPolicyError(password) || (password !== confirm ? 'Passwords do not match.' : ''); if (!error) authorize('password.change', async proof => { await identityAPI.post('password', { new_password: password, proof }); password = confirm = ''; returnToLogin(); }); }}>
        <label>New password<input type="password" bind:value={password} required autocomplete="new-password" /><span class="settings-note">At least 8 characters.</span></label><label>Confirm password<input type="password" bind:value={confirm} required autocomplete="new-password" /></label><button class="settings-button" disabled={!!purpose || busy}>Change password</button>
      </form>
    </section>
    <section class="settings-section"><h2 class="text-lg font-semibold">Passkeys</h2>
      {#if !storeAuth.passkeys}<p class="settings-note">Passkeys are unavailable on this server.</p>{:else}
      {#if keys.length === 0}<p class="settings-note">No passkeys saved yet.</p>{/if}
      <ul class="divide-y divide-gray-200 dark:divide-dark-border">{#each keys as key}<li class="flex flex-wrap items-center justify-between gap-3 py-3"><div><strong>{key.name}</strong><p class="settings-note">Last used: {key.last_used_at ? new Date(key.last_used_at).toLocaleString() : 'Never'}</p></div><button class="settings-button" disabled={!!purpose || busy} onclick={() => authorize('passkey.delete', async proof => { await identityAPI.post(`passkeys/${encodeURIComponent(key.id)}/delete`, { proof }); returnToLogin(); })}>Delete and sign out</button></li>{/each}</ul>
      <form class="flex flex-wrap items-end gap-3" onsubmit={e => { e.preventDefault(); authorize('passkey.enroll', async proof => { const { data } = await identityAPI.post('passkeys/enroll/begin', { name: keyName, proof }); const credential = await startRegistration(data.publicKey, controller.signal); if (!credential) throw new Error('Cancelled'); await identityAPI.post('passkeys/enroll/finish', credential); keyName = ''; await load(); }); }}><label class="flex-1">Passkey name<input bind:value={keyName} required maxlength="80" placeholder="Personal laptop" /></label><button class="settings-button" disabled={!!purpose || busy || !isWebAuthnSupported() || keys.length >= 20}>Add passkey</button></form>
      {/if}
    </section>
    <section class="settings-section"><h2 class="text-lg font-semibold">Linked accounts</h2><p class="settings-note">Link explicitly to use an external account for this identity. Matching email addresses do not merge accounts. A last usable sign-in method cannot be removed.</p>
      {#if !links.length}<p class="settings-note">No external accounts linked.</p>{/if}
      <ul class="divide-y divide-gray-200 dark:divide-dark-border">{#each links as link}<li class="py-3 space-y-2"><div class="flex flex-wrap justify-between gap-3"><strong>{providers.find(p => p.id === link.provider_id)?.label || link.provider_id}</strong><button class="settings-button" disabled={!!purpose || busy} onclick={() => authorize('identity.unlink', async proof => { await identityAPI.delete(`identities/${encodeURIComponent(link.id)}`, { data: { recent_proof: proof } }); returnToLogin(); })}>Unlink account</button></div><p class="settings-note break-all">{link.issuer} · {link.subject}</p>{#if link.email}<p class="settings-note">{link.email} {link.email_verified ? '(verified)' : '(unverified)'}</p>{/if}</li>{/each}</ul>
      <label class="max-w-md">Identity provider<select bind:value={provider}><option value="">Choose provider</option>{#each providers as p}<option value={p.id}>{p.label}</option>{/each}</select></label>
      {#if linkProof}<button class="settings-primary" disabled={!provider || busy} onclick={link}>Open provider and link account</button>{:else}<button class="settings-button" disabled={!provider || !!purpose || busy} onclick={() => authorize('identity.link', async proof => { linkProof = proof; })}>Verify to link account</button>{/if}
    </section>
    {/if}
</div>
