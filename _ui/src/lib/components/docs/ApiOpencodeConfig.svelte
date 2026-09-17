<script lang="ts">
  import { CheckSquare, Square } from 'lucide-svelte';
  import type { InfoProvider } from '@/lib/api/gateway';
  import DocsCodeBlock from './DocsCodeBlock.svelte';
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
      : opencodeProviderConfig({
          baseUrl,
          instanceName,
          models: Array.from(selectedModels),
        }),
  );

  const inputClass =
    'border border-gray-300 bg-white px-2 py-1.5 text-sm text-gray-900 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:focus-visible:outline-accent';
  const buttonClass =
    'inline-flex items-center gap-1.5 border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 transition-colors hover:bg-gray-100 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 motion-reduce:transition-none dark:border-dark-border-subtle dark:text-dark-text-secondary dark:hover:bg-dark-elevated dark:focus-visible:outline-accent';

  function modeClass(value: 'discovery' | 'manual'): string[] {
    return [
      buttonClass,
      mode === value
        ? 'bg-gray-900 text-white hover:bg-gray-900 dark:bg-accent dark:text-dark-bg dark:hover:bg-accent'
        : '',
    ];
  }
</script>

<div
  class="flex flex-wrap gap-2"
  role="group"
  aria-label="opencode model list source"
>
  <button
    type="button"
    aria-pressed={mode === 'discovery'}
    onclick={() => (mode = 'discovery')}
    class={modeClass('discovery')}
  >
    Auto discovery
  </button>
  <button
    type="button"
    aria-pressed={mode === 'manual'}
    onclick={() => (mode = 'manual')}
    class={modeClass('manual')}
  >
    Pick models
  </button>
</div>

{#if mode === 'discovery'}
  <p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
    The <code
      class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
      >opencode-models-discovery</code
    > plugin reads the model list from
    <code
      class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
      >/gateway/v1/models</code
    > at startup, so
    <code
      class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
      >models</code
    > stays empty and you never edit the file again when providers change. Paste the block into
    <code
      class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
      >~/.config/opencode/opencode.json</code
    > and run
    <code
      class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
      >opencode auth login</code
    > with a gateway API token — the list endpoint is authenticated.
  </p>
{:else}
  <p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
    Pick the models you want opencode to offer, then paste the generated block into
    <code
      class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
      >~/.config/opencode/opencode.json</code
    >. Use this when you want a fixed, curated list instead of everything the gateway advertises.
  </p>

  {#if loading}
    <p class="text-sm text-gray-600 dark:text-dark-text-secondary">Loading providers…</p>
  {:else if providers.length === 0}
    <p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
      No providers are configured yet, so the block below has no models. Add one on the
      <a
        href="#/providers"
        class="text-gray-900 underline underline-offset-2 hover:no-underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 dark:text-accent-text dark:focus-visible:outline-accent"
        >Providers</a
      > page.
    </p>
  {:else}
    <div class="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div>
        <label
          for="docs-opencode-provider"
          class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary"
        >
          Filter by provider
        </label>
        <select
          id="docs-opencode-provider"
          bind:value={selectedProviderKey}
          class={[inputClass, 'mt-1 w-full sm:w-56']}
        >
          <option value="">All providers</option>
          {#each providers as p (p.key)}
            <option value={p.key}>{p.key}</option>
          {/each}
        </select>
      </div>
      <div class="flex gap-2">
        <button type="button" onclick={() => bulk(true)} class={buttonClass}>
          <CheckSquare size={14} aria-hidden="true" />
          Select all
        </button>
        <button type="button" onclick={() => bulk(false)} class={buttonClass}>
          <Square size={14} aria-hidden="true" />
          Select none
        </button>
      </div>
    </div>

    {#each visibleProviders as p (p.key)}
      <fieldset class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3">
        <legend
          class="px-1 text-[11px] font-medium uppercase tracking-wider text-gray-600 dark:text-dark-text-secondary"
        >
          {p.key}
        </legend>
        <div class="grid grid-cols-1 gap-1.5 sm:grid-cols-2">
          {#each modelsOf(p) as m (m)}
            {@const fullId = `${p.key}/${m}`}
            <label
              class="flex cursor-pointer items-center gap-2 border border-gray-200 px-2 py-1.5 text-xs text-gray-700 transition-colors hover:border-gray-400 has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-gray-900 motion-reduce:transition-none dark:border-dark-border dark:text-dark-text-secondary dark:hover:border-dark-border-subtle dark:has-[:focus-visible]:outline-accent"
            >
              <input
                type="checkbox"
                checked={selectedModels.has(fullId)}
                onchange={() => toggleModel(fullId)}
                class="size-3.5 shrink-0 accent-gray-900 dark:accent-accent"
              />
              <span class="truncate font-mono">{m}</span>
            </label>
          {/each}
        </div>
      </fieldset>
    {/each}

    <p class="text-xs text-gray-600 dark:text-dark-text-secondary">
      {selectedModels.size} model{selectedModels.size === 1 ? '' : 's'} selected.
    </p>
  {/if}
{/if}

<DocsCodeBlock
  code={config}
  lang="json"
  label="~/.config/opencode/opencode.json"
  copyLabel="Copy opencode provider config"
/>
