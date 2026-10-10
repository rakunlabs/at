<script lang="ts">
  import type { InfoProvider } from '@/lib/api/gateway';
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import DocsCallout from './DocsCallout.svelte';
  import { opencodeDiscoveryConfig, opencodeProviderConfig } from './snippets';

  interface Props {
    baseUrl: string;
    instanceName: string;
    providers: InfoProvider[];
    loading?: boolean;
  }

  let { baseUrl, instanceName, providers, loading = false }: Props = $props();

  /** `discovery` asks opencode to read the model list at runtime. */
  let mode = $state<'discovery' | 'manual'>('discovery');
  let selectedProviderKey = $state('');
  /** Full model ids, `provider/model`. */
  let selectedModels = $state<Set<string>>(new Set());

  // Seed once, with each provider's default model, as soon as info arrives.
  // Plain `let` (not $state) so the effect does not depend on its own write.
  let seeded = false;
  $effect(() => {
    if (seeded || providers.length === 0) return;
    const defaults = new Set<string>();
    for (const p of providers) {
      if (p.default_model) defaults.add(`${p.key}/${p.default_model}`);
      else if (p.models && p.models.length > 0) defaults.add(`${p.key}/${p.models[0]}`);
    }
    selectedModels = defaults;
    seeded = true;
  });

  const visibleProviders = $derived(
    selectedProviderKey ? providers.filter((p) => p.key === selectedProviderKey) : providers,
  );

  function modelsOf(p: InfoProvider): string[] {
    return p.models && p.models.length > 0 ? p.models : [p.default_model];
  }

  function toggleModel(fullId: string) {
    const next = new Set(selectedModels);
    if (next.has(fullId)) next.delete(fullId);
    else next.add(fullId);
    selectedModels = next;
  }

  function bulk(select: boolean) {
    const next = new Set(selectedModels);
    for (const p of visibleProviders) {
      for (const m of modelsOf(p)) {
        if (select) next.add(`${p.key}/${m}`);
        else next.delete(`${p.key}/${m}`);
      }
    }
    selectedModels = next;
  }

  const config = $derived(
    mode === 'discovery'
      ? opencodeDiscoveryConfig({ baseUrl, instanceName })
      : opencodeProviderConfig({ baseUrl, instanceName, models: Array.from(selectedModels) }),
  );

  const modes = [
    { id: 'discovery' as const, label: 'Auto discovery', hint: 'Recommended' },
    { id: 'manual' as const, label: 'Pick models', hint: 'Fixed list' },
  ];
</script>

<div class="docs-prose">
  <p>
    Add this instance to <a href="https://opencode.ai" target="_blank" rel="noopener noreferrer">opencode</a>
    as an OpenAI-compatible provider. Paste the generated block into
    <code>~/.config/opencode/opencode.json</code>, then run <code>opencode auth login</code> and
    enter a gateway API token.
  </p>
</div>

<div role="radiogroup" aria-label="Model list source" class="grid gap-px border border-dark-border bg-dark-border sm:grid-cols-2">
  {#each modes as m (m.id)}
    <button
      type="button"
      role="radio"
      aria-checked={mode === m.id}
      onclick={() => (mode = m.id)}
      class={[
        'flex items-center justify-between gap-2 px-3 py-2 text-left text-sm focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent',
        mode === m.id ? 'bg-dark-elevated text-dark-text' : 'bg-dark-base text-dark-text-secondary hover:bg-dark-surface',
      ]}
    >
      <span class="flex items-center gap-2">
        <span
          class={['size-2 shrink-0 border', mode === m.id ? 'border-oc-peach bg-oc-peach' : 'border-dark-border-subtle']}
          aria-hidden="true"
        ></span>
        {m.label}
      </span>
      <span class="text-[11px] text-dark-text-muted">{m.hint}</span>
    </button>
  {/each}
</div>

{#if mode === 'discovery'}
  <div class="docs-prose">
    <p>
      The <code>opencode-models-discovery</code> plugin reads <code>/gateway/v1/models</code> and
      <code>/gateway/v1/model/info</code>, so <code>models</code> stays empty and the file never needs
      editing when providers change. Context and output limits, reasoning levels and the prices from
      the Pricing page come along with each model.
    </p>
    <ul>
      <li>The plugin is pinned to 1.9.0 and caches the catalog for 24 hours, so startup does not wait for the gateway.</li>
      <li>After changing providers or prices, run <code>/models-discovery-refresh</code> in opencode instead of waiting for the cache to expire.</li>
      <li>After updating an existing config, run <code>opencode service restart</code> once.</li>
    </ul>
  </div>
{:else}
  <div class="docs-prose">
    <p>Choose the models opencode should offer. Use this for a curated list instead of everything the gateway advertises.</p>
  </div>

  {#if loading}
    <p class="text-sm text-dark-text-muted">Loading providers…</p>
  {:else if providers.length === 0}
    <p class="text-sm leading-relaxed text-dark-text-secondary">
      No providers are configured yet, so the block below has no models. Add one on the
      <a href="#/providers" class="text-accent-text underline underline-offset-2">Providers</a> page.
    </p>
  {:else}
    <div class="flex flex-wrap items-center gap-2">
      <label for="docs-opencode-provider" class="sr-only">Filter by provider</label>
      <select
        id="docs-opencode-provider"
        bind:value={selectedProviderKey}
        class="h-8 border border-dark-border-subtle bg-dark-base px-2 text-xs text-dark-text focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
      >
        <option value="">All providers</option>
        {#each providers as p (p.key)}
          <option value={p.key}>{p.key}</option>
        {/each}
      </select>
      <button type="button" onclick={() => bulk(true)} class="settings-button">Select all</button>
      <button type="button" onclick={() => bulk(false)} class="settings-button">Select none</button>
      <span class="ml-auto text-xs text-dark-text-muted">
        {selectedModels.size} model{selectedModels.size === 1 ? '' : 's'} selected
      </span>
    </div>

    {#each visibleProviders as p (p.key)}
      <fieldset class="border border-dark-border">
        <legend class="sr-only">{p.key}</legend>
        <div class="border-b border-dark-border px-3 py-1.5 text-xs font-medium text-dark-text" aria-hidden="true">
          {p.key}
        </div>
        <div class="grid gap-px bg-dark-border sm:grid-cols-2">
          {#each modelsOf(p) as m (m)}
            {@const fullId = `${p.key}/${m}`}
            <label
              class="flex cursor-pointer items-center gap-2 bg-dark-base px-3 py-1.5 text-xs text-dark-text-secondary hover:bg-dark-surface has-[:focus-visible]:outline-2 has-[:focus-visible]:-outline-offset-2 has-[:focus-visible]:outline-accent"
            >
              <input
                type="checkbox"
                checked={selectedModels.has(fullId)}
                onchange={() => toggleModel(fullId)}
                class="size-3.5 shrink-0 accent-accent"
              />
              <span class="truncate font-mono">{m}</span>
            </label>
          {/each}
        </div>
      </fieldset>
    {/each}
  {/if}
{/if}

<DocsCodeBlock code={config} lang="json" label="~/.config/opencode/opencode.json" copyLabel="Copy opencode provider config" />

<DocsCallout>
  <p>
    To give opencode AT’s tools as well, add an MCP server next to the provider — see
    <a href="#/docs?section=mcp-configuration">MCP servers</a>.
  </p>
</DocsCallout>
