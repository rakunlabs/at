<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { applyFeature, loadFeatures, storeFeatures } from '@/lib/store/features.svelte';
  import { updateFeature, type Feature } from '@/lib/api/features';
  import { Loader2, Power, ToggleLeft, ToggleRight } from 'lucide-svelte';

  storeNavbar.title = 'Features';

  let savingKey = $state('');

  $effect(() => {
    loadFeatures().catch((e: any) => {
      addToast(e?.response?.data?.message || 'Failed to load features', 'alert');
    });
  });

  async function toggleFeature(feature: Feature) {
    if (savingKey) return;

    savingKey = feature.key;
    try {
      const updated = await updateFeature(feature.key, !feature.enabled);
      applyFeature(updated);
      addToast(`${updated.name} ${updated.enabled ? 'enabled' : 'disabled'}`);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update feature', 'alert');
    } finally {
      savingKey = '';
    }
  }
</script>

<svelte:head>
  <title>AT | Features</title>
</svelte:head>

<div class="p-6 max-w-5xl mx-auto space-y-4">
  <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
    <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border flex items-center gap-2">
      <Power size={14} class="text-gray-500 dark:text-dark-text-muted" />
      <h3 class="text-sm font-medium text-gray-900 dark:text-dark-text">Feature Controls</h3>
    </div>
    <div class="p-4">
      <p class="text-sm text-gray-600 dark:text-dark-text-secondary leading-relaxed">
        Disable modules to hide them from the admin UI and block their related API actions. Core gateway traffic remains available unless the specific feature controls that admin surface.
      </p>
    </div>
  </div>

  {#if storeFeatures.loading && !storeFeatures.loaded}
    <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-6 flex items-center gap-2 text-sm text-gray-600 dark:text-dark-text-secondary">
      <Loader2 size={14} class="animate-spin" />
      Loading features...
    </div>
  {:else}
    {#each storeFeatures.groups as group}
      <section class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
        <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-dark-text">{group.name}</h3>
          <p class="text-xs text-gray-500 dark:text-dark-text-muted mt-0.5">{group.description}</p>
        </div>

        <div class="divide-y divide-gray-100 dark:divide-dark-border-subtle">
          {#each group.features as feature}
            <div class="p-4 flex items-start justify-between gap-4">
              <div class="min-w-0">
                <div class="flex items-center gap-2">
                  <h4 class="text-sm font-medium text-gray-900 dark:text-dark-text">{feature.name}</h4>
                  <span class={[
                    'px-1.5 py-0.5 text-[10px] font-medium border',
                    feature.enabled
                      ? 'bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-400 border-green-200 dark:border-green-900/40'
                      : 'bg-gray-50 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted border-gray-200 dark:border-dark-border-subtle',
                  ]}>
                    {feature.enabled ? 'Enabled' : 'Disabled'}
                  </span>
                </div>
                <p class="text-xs text-gray-600 dark:text-dark-text-secondary leading-relaxed mt-1">{feature.description}</p>
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
                disabled={savingKey === feature.key}
                class={[
                  'shrink-0 inline-flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border transition-colors disabled:opacity-50',
                  feature.enabled
                    ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent hover:bg-gray-800 dark:hover:bg-accent-hover'
                    : 'bg-white dark:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary border-gray-200 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-surface',
                ]}
              >
                {#if savingKey === feature.key}
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
  {/if}
</div>
