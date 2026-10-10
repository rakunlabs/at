<script lang="ts">
  import { Copy, Check, Search } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import type { InfoProvider } from '@/lib/api/gateway';
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import { curlModelsExample } from './snippets';

  interface Props {
    baseUrl: string;
    providers: InfoProvider[];
    loading?: boolean;
    error?: string;
    onretry?: () => void;
  }

  let { baseUrl, providers, loading = false, error = '', onretry }: Props = $props();

  let filter = $state('');
  let copiedId = $state('');
  let timer: ReturnType<typeof setTimeout> | null = null;

  const groups = $derived.by(() => {
    const q = filter.trim().toLowerCase();
    return providers
      .map((p) => {
        const models = p.models && p.models.length > 0 ? p.models : [p.default_model];
        const ids = models.filter(Boolean).map((m) => `${p.key}/${m}`);
        return { key: p.key, type: p.type, ids: q ? ids.filter((id) => id.toLowerCase().includes(q)) : ids };
      })
      .filter((g) => g.ids.length > 0);
  });
  const total = $derived(groups.reduce((n, g) => n + g.ids.length, 0));

  async function copy(model: string) {
    try {
      await navigator.clipboard.writeText(model);
      copiedId = model;
      if (timer) clearTimeout(timer);
      timer = setTimeout(() => (copiedId = ''), 2000);
    } catch {
      addToast('Failed to copy to clipboard', 'alert');
    }
  }
</script>

<div class="docs-prose">
  <p>
    A model id is <code>provider_key/model_name</code>. The provider key is the name the provider
    has on the <a href="#/providers">Providers</a> page, so two OpenAI accounts can sit side by side
    as <code>openai/…</code> and <code>openai-eu/…</code>. A name without a slash is resolved as a
    <a href="#/docs?section=routing">routing profile</a>.
  </p>
  <p>
    Clients discover models with <code>GET /gateway/v1/models</code> (OpenAI shape, sorted, filtered
    to what the token may use). <code>GET /gateway/v1/model/info</code> returns the same list in
    LiteLLM shape with context limits, reasoning levels and prices.
  </p>
</div>

<DocsCodeBlock code={curlModelsExample(baseUrl)} lang="bash" label="curl" copyLabel="Copy list-models request" />

<section class="space-y-3 pt-2">
  <div class="flex flex-wrap items-end justify-between gap-3">
    <div>
      <h2 class="text-sm font-semibold text-dark-text">On this instance</h2>
      <p class="mt-0.5 text-xs text-dark-text-muted">
        Models your current account can use in this workspace. A token may see fewer.
      </p>
    </div>
    {#if !loading && !error && providers.length > 0}
      <div class="relative w-full sm:w-64">
        <Search
          size={13}
          class="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-dark-text-muted"
          aria-hidden="true"
        />
        <input
          type="search"
          bind:value={filter}
          aria-label="Filter models"
          placeholder="Filter models…"
          class="h-8 w-full border border-dark-border-subtle bg-dark-base pl-7 pr-2 text-xs text-dark-text placeholder:text-dark-text-muted focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
        />
      </div>
    {/if}
  </div>

  {#if loading}
    <p class="text-sm text-dark-text-muted">Loading models…</p>
  {:else if error}
    <div role="alert" class="border border-oc-peach/40 px-3 py-2.5">
      <p class="text-sm text-oc-peach">{error}</p>
      {#if onretry}
        <button type="button" onclick={onretry} class="settings-button mt-2">Retry</button>
      {/if}
    </div>
  {:else if providers.length === 0}
    <p class="text-sm leading-relaxed text-dark-text-secondary">
      No models are available yet. Configure a provider on the
      <a href="#/providers" class="text-accent-text underline underline-offset-2">Providers</a> page.
    </p>
  {:else if total === 0}
    <p class="text-sm text-dark-text-muted">No model matches “{filter}”.</p>
  {:else}
    {#each groups as g (g.key)}
      <div class="border border-dark-border">
        <div class="flex items-center justify-between border-b border-dark-border px-3 py-1.5">
          <span class="text-xs font-medium text-dark-text">{g.key}</span>
          <span class="text-[11px] text-dark-text-muted">{g.type} · {g.ids.length}</span>
        </div>
        <ul class="divide-y divide-dark-border">
          {#each g.ids as id (id)}
            <li class="flex items-center">
              <code class="min-w-0 flex-1 truncate px-3 py-1.5 font-mono text-[12px] text-dark-text-secondary">
                {id}
              </code>
              <button
                type="button"
                onclick={() => copy(id)}
                aria-label={`Copy model id ${id}`}
                class="shrink-0 px-3 py-1.5 text-dark-text-muted hover:bg-dark-surface hover:text-dark-text focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
              >
                {#if copiedId === id}
                  <Check size={12} class="text-oc-green" aria-hidden="true" />
                {:else}
                  <Copy size={12} aria-hidden="true" />
                {/if}
              </button>
            </li>
          {/each}
        </ul>
      </div>
    {/each}
  {/if}
</section>
