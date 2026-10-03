<script lang="ts">
  import JsonNode from './JsonNode.svelte';
  import { Copy, Search } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';

  interface Props {
    /** Raw text; parsed as JSON when possible, otherwise shown verbatim. */
    raw?: string;
    /** An already-parsed value (takes precedence over raw). */
    value?: unknown;
    /** Expand objects up to this depth initially. */
    depth?: number;
    maxHeight?: string;
    searchable?: boolean;
  }
  let { raw = '', value, depth = 2, maxHeight = 'max-h-96', searchable = true }: Props = $props();

  const parsed = $derived.by<{ ok: boolean; value: unknown }>(() => {
    if (value !== undefined) return { ok: true, value };
    if (!raw) return { ok: false, value: '' };
    try {
      return { ok: true, value: JSON.parse(raw) };
    } catch {
      return { ok: false, value: raw };
    }
  });
  let query = $state('');
  const text = $derived(parsed.ok ? JSON.stringify(parsed.value, null, 2) : String(parsed.value ?? ''));

  async function copyAll() {
    try {
      await navigator.clipboard.writeText(text);
      addToast('Copied to clipboard', 'info');
    } catch {
      addToast('Copy failed', 'alert');
    }
  }
</script>

<div class="border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
  <div class="flex items-center gap-2 border-b border-gray-200 dark:border-dark-border px-2 py-1">
    {#if searchable && parsed.ok}
      <Search size={12} class="shrink-0 text-gray-400 dark:text-dark-text-muted" />
      <input bind:value={query} placeholder="Filter keys and values…" aria-label="Filter JSON" class="min-w-0 flex-1 bg-transparent text-[11px] text-gray-700 outline-none placeholder:text-gray-400 dark:text-dark-text-secondary dark:placeholder:text-dark-text-muted" />
    {:else}
      <span class="flex-1"></span>
    {/if}
    <button onclick={copyAll} class="flex items-center gap-1 text-[11px] text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text-secondary" title="Copy"><Copy size={11} /> Copy</button>
  </div>
  <div class={['overflow-auto p-2 font-mono text-[11px] leading-relaxed text-gray-800 dark:text-dark-text-secondary', maxHeight]}>
    {#if parsed.ok}
      <JsonNode value={parsed.value} {depth} query={query.trim().toLowerCase()} level={0} />
    {:else}
      <pre class="whitespace-pre-wrap break-words">{text}</pre>
    {/if}
  </div>
</div>
