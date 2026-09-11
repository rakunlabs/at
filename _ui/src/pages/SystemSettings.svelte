<script lang="ts">
  import { storeInfo, storeNavbar } from '../lib/store/store.svelte';
  import { rotateKey } from '../lib/api/admin';
  import { isNativeAdmin } from '../lib/store/auth.svelte';
  let key = $state(''); let confirm = $state(''); let error = $state(''); let notice = $state(''); let busy = $state(false);
  storeNavbar.title = 'System';
  async function rotate(e: SubmitEvent) { e.preventDefault(); error = notice = ''; if (!key || key !== confirm) { error = 'Enter matching encryption passphrases.'; return; } busy = true; try { await rotateKey('', key); notice = 'Encryption key rotated.'; } catch { error = 'Key rotation failed. Check server availability and installation administrator access.'; } finally { busy = false; key = confirm = ''; } }
</script>
<div class="settings-page settings-form"><header><h1 class="text-2xl font-semibold">System</h1><p class="settings-note mt-2">Installation information and encryption management.</p></header>
  <dl class="grid sm:grid-cols-2 gap-5">{#each [['Application',storeInfo.name],['Version',storeInfo.version],['Commit',storeInfo.commit],['Build date',storeInfo.build_date],['Store',storeInfo.store_type],['Assets root',storeInfo.assets_root]] as [label,value]}<div><dt class="settings-note">{label}</dt><dd class="mt-1 break-all">{value || 'Unavailable'}</dd></div>{/each}</dl>
  {#if isNativeAdmin()}<section class="settings-section"><h2 class="text-lg font-semibold">Rotate encryption key</h2><p class="settings-note">Re-encrypt stored credentials with a new passphrase. Your installation administrator session authorizes this operation.</p><form class="space-y-4 max-w-lg" onsubmit={rotate}><label>New encryption passphrase<input type="password" bind:value={key} required autocomplete="new-password" /></label><label>Confirm passphrase<input type="password" bind:value={confirm} required autocomplete="new-password" /></label><button class="settings-primary" disabled={busy}>{busy ? 'Rotating…' : 'Rotate encryption key'}</button></form></section>{/if}
  {#if error}<p role="alert" class="settings-error">{error}</p>{/if}{#if notice}<p role="status" class="settings-note">{notice}</p>{/if}
</div>
