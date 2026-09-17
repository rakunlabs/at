<script lang="ts">
  import AuthShell from './AuthShell.svelte';
  import { onMount } from 'svelte';
  import { identityAPI } from '../api/identity';
  import { passwordPolicyError } from '../api/auth';
  let { ticket, oncomplete }: { ticket: string; oncomplete: () => void } = $props();
  let valid = $state(false); let busy = $state(true); let password = $state(''); let confirm = $state(''); let error = $state('');
  async function inspect() { busy = true; try { valid = (await identityAPI.post('recovery/inspect', { ticket })).data.valid === true; if (!valid) throw new Error(); } catch { error = 'This recovery link could not be verified. It may be expired or already used. Ask the administrator for a new link.'; } finally { busy = false; } }
  onMount(() => { void inspect(); return () => { password = confirm = ''; }; });
  async function redeem(e: SubmitEvent) { e.preventDefault(); error = passwordPolicyError(password) || (password !== confirm ? 'Passwords do not match.' : ''); if (error || busy) return; busy = true;
    try { await identityAPI.post('recovery/redeem', { ticket, password }); password = confirm = ''; oncomplete(); }
    catch { error = 'Recovery could not be completed. Request a new link if this one has expired or was already used.'; }
    finally { busy = false; }
  }
</script>
<AuthShell title="Recover your account" subtitle="Set a new local password for this installation." width="md">
  <p class="settings-note">Recovery removes your old passwords, passkeys, linked accounts, authenticator and sessions. Your workspaces and data are preserved.</p>
  {#if valid}<form class="space-y-4" onsubmit={redeem}><label>New password<input type="password" bind:value={password} required autocomplete="new-password" /><span class="settings-note">At least 8 characters.</span></label><label>Confirm password<input type="password" bind:value={confirm} required autocomplete="new-password" /></label><button class="settings-primary w-full min-h-11 sm:min-h-0" disabled={busy}>{busy ? 'Recovering…' : 'Reset credentials'}</button></form>{:else if busy}<p role="status" class="settings-note">Checking recovery link…</p>{/if}
  {#if error}<p role="alert" class="settings-error">{error}</p>{/if}
  <div class="border-t border-gray-100 dark:border-dark-border pt-4"><button class="settings-button" disabled={busy} onclick={oncomplete}>Return to sign-in</button></div>
</AuthShell>
