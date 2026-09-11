<script lang="ts">
  import { Link2, Check } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { docsShareUrl, type DocsSelection } from '@/lib/helper/docs-nav';

  interface Props {
    selection: DocsSelection;
  }

  let { selection }: Props = $props();

  let copied = $state(false);
  let timer: ReturnType<typeof setTimeout> | null = null;

  async function copy() {
    const url = docsShareUrl(selection, window.location.origin, window.location.pathname);
    try {
      await navigator.clipboard.writeText(url);
      copied = true;
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => (copied = false), 2000);
      addToast('Link copied to clipboard');
    } catch {
      addToast('Failed to copy link', 'alert');
    }
  }
</script>

<button
  type="button"
  onclick={copy}
  class="inline-flex items-center gap-1.5 border border-gray-300 px-2 py-1 text-xs font-medium text-gray-700 transition-colors hover:bg-gray-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 motion-reduce:transition-none dark:border-dark-border-subtle dark:text-dark-text-secondary dark:hover:bg-dark-elevated dark:focus-visible:outline-accent"
>
  {#if copied}
    <Check size={12} aria-hidden="true" />
    Copied
  {:else}
    <Link2 size={12} aria-hidden="true" />
    Copy link
  {/if}
</button>
