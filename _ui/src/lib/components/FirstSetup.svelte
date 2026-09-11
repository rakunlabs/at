<script lang="ts">
  import { identityAPI } from '../api/identity';
  import { passwordPolicyError, authErrorMessage } from '../api/auth';
  let { oncomplete }: { oncomplete: () => Promise<void> } = $props();
  let username = $state(''); let password = $state(''); let confirm = $state(''); let busy = $state(false); let error = $state('');
  async function submit(event: SubmitEvent) {
    event.preventDefault(); error = passwordPolicyError(password) || (password !== confirm ? 'Passwords do not match.' : '');
    if (error || busy) return; busy = true;
    try { await identityAPI.post('setup', { username, password, origin: location.origin }); password = confirm = ''; await oncomplete(); }
    catch (e) { error = authErrorMessage(e, 'Could not create the administrator. Retry, or reload if setup was already completed.'); }
    finally { busy = false; password = confirm = ''; }
  }
</script>
<main class="min-h-full flex items-center justify-center px-6 py-12"><div class="w-full max-w-md settings-form">
  <h1 class="text-2xl font-semibold">Set up AT</h1><p class="settings-note mt-2">Create the first local administrator for this installation. After setup, sign in to configure your workspace.</p>
  <form onsubmit={submit} class="space-y-5 mt-8">
    <label>Administrator username<input bind:value={username} required minlength="3" maxlength="128" pattern={'[a-zA-Z0-9._@+\\-]{3,128}'} autocomplete="username" autocapitalize="none" /></label>
    <label>Password<input type="password" bind:value={password} required autocomplete="new-password" /><span class="settings-note">Use at least 8 characters.</span></label>
    <label>Confirm password<input type="password" bind:value={confirm} required autocomplete="new-password" /></label>
    <p class="settings-note">Server origin: <code class="break-all">{location.origin}</code></p>
    {#if error}<p role="alert" class="settings-error">{error}</p>{/if}
    <button class="settings-primary w-full" disabled={busy}>{busy ? 'Creating administrator…' : 'Create administrator'}</button>
  </form>
</div></main>
