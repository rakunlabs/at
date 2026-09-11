<script lang="ts">
  import { securityCodes, storeAuth, returnToLogin } from '../store/auth.svelte';
  import { downloadSecret } from '../helper/recovery';
  let saved = $state(false);
</script>
<main class="min-h-full flex justify-center items-center px-6 py-12"><section class="max-w-lg w-full settings-form space-y-5" aria-label="Save backup codes">
  <h1 class="text-2xl font-semibold">Save your backup codes</h1>
  <p class="settings-note">These codes are shown once. Each replaces an authenticator code for one sign-in, after your primary sign-in method. All sessions have ended. Save the codes before closing this page.</p>
  <ul class="grid sm:grid-cols-2 gap-3 font-mono select-all">{#each securityCodes.values as code}<li class="border border-gray-200 dark:border-dark-border rounded-md p-3">{code}</li>{/each}</ul>
  <button class="settings-primary" onclick={() => downloadSecret('at-backup-codes.txt', 'AT backup codes — keep private. Each code is single-use.\n\n' + securityCodes.values.join('\n'))}>Download backup codes</button>
  <label><input type="checkbox" bind:checked={saved} />I saved these codes somewhere private</label>
  <button class="settings-button" disabled={!saved} onclick={() => { securityCodes.values = []; storeAuth.securityHold = false; returnToLogin(); }}>Continue to sign-in</button>
</section></main>
