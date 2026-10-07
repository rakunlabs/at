<script lang="ts">
  import { Download, ExternalLink, X } from 'lucide-svelte';

  interface Props {
    src: string;
    alt?: string;
    onclose: () => void;
  }
  let { src, alt = '', onclose }: Props = $props();

  let panel: HTMLDivElement | undefined = $state();
  $effect(() => { panel?.focus(); });

  // data: URLs cannot be opened in a new tab by most browsers, so only offer it for real URLs.
  const openable = $derived(!src.startsWith('data:'));
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  bind:this={panel}
  tabindex="-1"
  role="dialog"
  aria-modal="true"
  aria-label={alt || 'Image preview'}
  class="fixed inset-0 z-[60] flex flex-col bg-black/85 focus:outline-none"
  onclick={(e) => { if (e.target === e.currentTarget) onclose(); }}
  onkeydown={(e) => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onclose(); } }}
>
  <div class="flex shrink-0 items-center gap-3 px-4 py-3 text-xs text-dark-text-secondary">
    <span class="min-w-0 flex-1 truncate">{alt}</span>
    {#if openable}
      <a href={src} target="_blank" rel="noopener noreferrer" aria-label="Open in new tab" title="Open in new tab" class="p-1 hover:bg-dark-elevated hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent"><ExternalLink size={16} /></a>
    {/if}
    <a href={src} download={alt || 'image'} aria-label="Download" title="Download" class="p-1 hover:bg-dark-elevated hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent"><Download size={16} /></a>
    <button onclick={onclose} aria-label="Close" title="Close (Esc)" class="p-1 hover:bg-dark-elevated hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent"><X size={16} /></button>
  </div>
  <div class="flex min-h-0 flex-1 items-center justify-center p-4 pt-0" onclick={(e) => { if (e.target === e.currentTarget) onclose(); }}>
    <img {src} {alt} class="max-h-full max-w-full object-contain" />
  </div>
</div>
