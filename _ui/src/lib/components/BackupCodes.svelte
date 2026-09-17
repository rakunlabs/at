<script lang="ts">
  import AuthShell from './AuthShell.svelte';
  import { securityCodes, storeAuth, returnToLogin } from '../store/auth.svelte';
  import { downloadSecret } from '../helper/recovery';
  let saved = $state(false);
</script>
<AuthShell title="Save your backup codes" subtitle="Shown once — they cannot be displayed again." width="lg" label="Save backup codes">
  <p class="settings-note">Each code replaces an authenticator code for one sign-in, after your primary sign-in method. All sessions have ended. Save the codes before closing this page.</p>
  <ul class="grid sm:grid-cols-2 gap-px bg-gray-200 dark:bg-dark-border border border-gray-200 dark:border-dark-border font-mono text-xs select-all">{#each securityCodes.values as code}<li class="bg-white dark:bg-dark-elevated px-3 py-2">{code}</li>{/each}</ul>
  <button class="settings-primary w-full min-h-11 sm:min-h-0" onclick={() => downloadSecret('at-backup-codes.txt', 'AT backup codes — keep private. Each code is single-use.\n\n' + securityCodes.values.join('\n'))}>Download backup codes</button>
  <div class="border-t border-gray-100 dark:border-dark-border pt-4 space-y-3">
    <label><input type="checkbox" bind:checked={saved} />I saved these codes somewhere private</label>
    <button class="settings-button w-full min-h-11 sm:min-h-0" disabled={!saved} onclick={() => { securityCodes.values = []; storeAuth.securityHold = false; returnToLogin(); }}>Continue to sign-in</button>
  </div>
</AuthShell>
