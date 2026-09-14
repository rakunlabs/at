<script lang="ts">
  import { Download } from 'lucide-svelte';
  import { pwa } from '../store/pwa.svelte';
  let installing = $state(false);
  let error = $state('');
  async function install() {
    const prompt = pwa.prompt;
    if (!prompt) return;
    installing = true;
    error = '';
    try { await prompt.prompt(); await prompt.userChoice; }
    catch { error = 'Installation did not open. Try the browser’s install menu.'; }
    finally { pwa.prompt = null; installing = false; }
  }
</script>

{#if !pwa.installed}
  <div class="px-2 py-3 border-t border-gray-200 dark:border-dark-border">
    {#if pwa.prompt}
      <button class="settings-button flex min-h-11 w-full items-center gap-2" disabled={installing} onclick={install}><Download size={16} />{installing ? 'Installing…' : 'Install AT'}</button>
    {:else}
      <details class="text-xs text-gray-600 dark:text-dark-text-secondary">
        <summary class="flex min-h-11 cursor-pointer items-center gap-2 rounded-md px-2 focus-visible:outline-2 focus-visible:outline-accent"><Download size={16} />Install AT</summary>
        <p class="px-2 pt-2 leading-5">On iPhone or iPad, open AT in Safari, tap Share, then Add to Home Screen. On other devices, use your browser’s Install app menu. Installation requires HTTPS or localhost.</p>
      </details>
    {/if}
    {#if error}<p role="alert" class="settings-error mt-2">{error}</p>{/if}
  </div>
{/if}
