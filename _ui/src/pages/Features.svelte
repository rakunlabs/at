<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { applyFeatures, loadFeatures, storeFeatures } from '@/lib/store/features.svelte';
  import { applyFeaturePreset, updateFeatures, type Feature, type FeatureGroup } from '@/lib/api/features';
  import { Loader2, Power, Search, ToggleLeft, ToggleRight } from 'lucide-svelte';

  storeNavbar.title = 'Features';

  let savingKey = $state('');
  let busy = $state('');
  let search = $state('');

  $effect(() => {
    loadFeatures().catch((e: any) => {
      addToast(e?.response?.data?.message || 'Failed to load features', 'alert');
    });
  });

  const query = $derived(search.trim().toLowerCase());

  const visibleGroups = $derived(
    storeFeatures.groups
      .map((group) => ({
        ...group,
        features: query
          ? group.features.filter((f) =>
              f.name.toLowerCase().includes(query) ||
              f.key.includes(query) ||
              f.description.toLowerCase().includes(query))
          : group.features,
      }))
      .filter((group) => group.features.length > 0),
  );

  const nameByKey = $derived(
    Object.fromEntries(storeFeatures.features.map((f) => [f.key, f.name])) as Record<string, string>,
  );

  function enabledCount(group: FeatureGroup) {
    return group.features.filter((f) => f.effective).length;
  }

  // Every write goes through the bulk endpoint: toggling a parent changes what
  // its descendants resolve to, so the server answers with the whole catalog
  // rather than one row and the page never has to recompute that itself.
  async function write(label: string, busyKey: string, run: () => Promise<any>) {
    if (busy || savingKey) return;
    busy = busyKey;
    try {
      applyFeatures(await run());
      addToast(label);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update features', 'alert');
    } finally {
      busy = '';
      savingKey = '';
    }
  }

  async function toggleFeature(feature: Feature) {
    savingKey = feature.key;
    await write(
      `${feature.name} ${feature.enabled ? 'disabled' : 'enabled'}`,
      feature.key,
      () => updateFeatures({ [feature.key]: !feature.enabled }),
    );
  }

  async function setGroup(group: FeatureGroup, enabled: boolean) {
    const payload: Record<string, boolean> = {};
    for (const feature of group.features) payload[feature.key] = enabled;
    await write(`${group.name} ${enabled ? 'enabled' : 'disabled'}`, `group:${group.key}`, () => updateFeatures(payload));
  }

  async function setAll(enabled: boolean) {
    const payload: Record<string, boolean> = {};
    for (const feature of storeFeatures.features) payload[feature.key] = enabled;
    await write(enabled ? 'All features enabled' : 'All features disabled', `all:${enabled}`, () => updateFeatures(payload));
  }

  async function usePreset(key: string, name: string) {
    await write(`Applied preset: ${name}`, `preset:${key}`, () => applyFeaturePreset(key));
  }
</script>

<svelte:head>
  <title>AT | Features</title>
</svelte:head>

<div class="p-6 max-w-6xl mx-auto space-y-4">
  <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
    <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border flex items-center gap-2">
      <Power size={14} class="text-gray-500 dark:text-dark-text-muted" />
      <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">Feature Controls</h3>
    </div>
    <div class="p-4 space-y-3">
      <p class="text-sm text-gray-600 dark:text-dark-text-secondary leading-relaxed">
        Disable modules to hide them from the admin UI and block their related API actions. Core gateway traffic
        (<code class="text-xs">/gateway/v1/…</code>) remains available regardless. Features are nested: a child is
        unavailable while its parent is off, and turning the parent back on restores each child to its own setting.
      </p>
      <p class="text-xs text-gray-500 dark:text-dark-text-muted">
        This page is never gated, so any combination below is reversible from here.
      </p>
    </div>
  </div>

  {#if storeFeatures.loading && !storeFeatures.loaded}
    <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-6 flex items-center gap-2 text-sm text-gray-600 dark:text-dark-text-secondary">
      <Loader2 size={14} class="animate-spin" />
      Loading features...
    </div>
  {:else}
    {#if storeFeatures.presets.length}
      <section class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-dark-text">Presets</h3>
          <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-0.5">
            Each preset writes an explicit state for every feature — the ones it does not list are switched off.
          </p>
        </div>
        <div class="p-4 grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
          {#each storeFeatures.presets as preset}
            <button
              type="button"
              onclick={() => usePreset(preset.key, preset.name)}
              disabled={!!busy}
              class="text-left border border-gray-200 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated p-3 hover:bg-gray-50 dark:hover:bg-dark-surface disabled:opacity-50 transition-colors"
            >
              <div class="flex items-center gap-1.5">
                {#if busy === `preset:${preset.key}`}
                  <Loader2 size={12} class="animate-spin" />
                {/if}
                <span class="text-xs font-medium text-gray-900 dark:text-dark-text">{preset.name}</span>
              </div>
              <p class="text-[11px] text-gray-500 dark:text-dark-text-muted leading-relaxed mt-1">{preset.description}</p>
            </button>
          {/each}
        </div>
      </section>
    {/if}

    <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface px-4 py-3 flex flex-wrap items-center gap-2">
      <div class="relative flex-1 min-w-48">
        <Search size={13} class="absolute left-2 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-text-muted" />
        <input
          type="search"
          bind:value={search}
          placeholder="Filter features"
          class="w-full pl-7 pr-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated text-gray-900 dark:text-dark-text focus-visible:outline-2 focus-visible:outline-accent"
        />
      </div>
      <button
        type="button"
        onclick={() => setAll(true)}
        disabled={!!busy}
        class="px-3 py-1.5 text-xs font-medium border border-gray-200 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-surface disabled:opacity-50"
      >
        Enable all
      </button>
      <button
        type="button"
        onclick={() => setAll(false)}
        disabled={!!busy}
        class="px-3 py-1.5 text-xs font-medium border border-gray-200 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-surface disabled:opacity-50"
      >
        Disable all
      </button>
    </div>

    {#each visibleGroups as group}
      <section class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base flex items-start justify-between gap-3">
          <div class="min-w-0">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-dark-text">
              {group.name}
              <span class="ml-1 text-[10px] font-normal text-gray-400 dark:text-dark-text-muted">
                {enabledCount(group)}/{group.features.length} on
              </span>
            </h3>
            <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-0.5">{group.description}</p>
          </div>
          <div class="shrink-0 flex items-center gap-1">
            {#if busy === `group:${group.key}`}
              <Loader2 size={13} class="animate-spin text-gray-400" />
            {/if}
            <button
              type="button"
              onclick={() => setGroup(group, true)}
              disabled={!!busy}
              class="px-2 py-1 text-[11px] border border-gray-200 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-surface disabled:opacity-50"
            >
              All on
            </button>
            <button
              type="button"
              onclick={() => setGroup(group, false)}
              disabled={!!busy}
              class="px-2 py-1 text-[11px] border border-gray-200 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-surface disabled:opacity-50"
            >
              All off
            </button>
          </div>
        </div>

        <div class="divide-y divide-gray-100 dark:divide-dark-border-subtle">
          {#each group.features as feature}
            <div class={[
              'p-4 flex items-start justify-between gap-4',
              feature.parent ? 'pl-8 border-l-2 border-l-gray-200 dark:border-l-dark-border-subtle' : '',
            ]}>
              <div class="min-w-0">
                <div class="flex items-center gap-2 flex-wrap">
                  <h4 class="text-sm font-medium text-gray-900 dark:text-dark-text">{feature.name}</h4>
                  <span class={[
                    'px-1.5 py-0.5 text-[10px] font-medium border',
                    feature.effective
                      ? 'bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-400 border-green-200 dark:border-green-900/40'
                      : 'bg-gray-50 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted border-gray-200 dark:border-dark-border-subtle',
                  ]}>
                    {feature.effective ? 'Enabled' : 'Disabled'}
                  </span>
                  <code class="text-[10px] text-gray-400 dark:text-dark-text-muted">{feature.key}</code>
                </div>
                <p class="text-xs text-gray-600 dark:text-dark-text-secondary leading-relaxed mt-1">{feature.description}</p>
                {#if feature.blocked_by}
                  <p class="text-[11px] text-amber-700 dark:text-amber-400 mt-1.5">
                    Unavailable while <span class="font-medium">{nameByKey[feature.blocked_by] || feature.blocked_by}</span> is off{feature.enabled ? ' — its own switch stays on and takes effect again when the parent returns' : ''}.
                  </p>
                {/if}
                {#if feature.updated_at}
                  <p class="text-[10px] text-gray-400 dark:text-dark-text-muted mt-2">
                    Last changed {new Date(feature.updated_at).toLocaleString()}{feature.updated_by ? ` by ${feature.updated_by}` : ''}
                  </p>
                {/if}
              </div>

              <button
                type="button"
                role="switch"
                aria-checked={feature.enabled}
                onclick={() => toggleFeature(feature)}
                disabled={!!busy}
                class={[
                  'shrink-0 inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border transition-colors disabled:opacity-50',
                  feature.enabled
                    ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent hover:bg-gray-800 dark:hover:bg-accent-hover'
                    : 'bg-white dark:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary border-gray-200 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-surface',
                ]}
              >
                {#if savingKey === feature.key && busy}
                  <Loader2 size={13} class="animate-spin" />
                  Saving
                {:else if feature.enabled}
                  <ToggleRight size={13} />
                  On
                {:else}
                  <ToggleLeft size={13} />
                  Off
                {/if}
              </button>
            </div>
          {/each}
        </div>
      </section>
    {/each}

    {#if query && visibleGroups.length === 0}
      <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-6 text-sm text-gray-500 dark:text-dark-text-muted">
        No feature matches “{search}”.
      </div>
    {/if}
  {/if}
</div>
