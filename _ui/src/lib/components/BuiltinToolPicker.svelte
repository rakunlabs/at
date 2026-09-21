<script lang="ts">
  import type { BuiltinToolDef } from '@/lib/api/mcp';
  import { builtinFamilies, builtinFamily, builtinDisabledBy, builtinGroupLabel } from '@/lib/helper/builtin-tools';
  import { isFeatureEnabled } from '@/lib/store/features.svelte';

  let { tools, selected = $bindable([]), inherited = [], onchange }: {
    tools: BuiltinToolDef[];
    selected?: string[];
    inherited?: string[];
    onchange?: () => void;
  } = $props();

  let search = $state('');
  let missing = $derived([...new Set([...selected, ...inherited])].filter(name => !tools.some(t => t.name === name)));
  function toggle(tool: BuiltinToolDef) {
    if (builtinDisabledBy(tool, isFeatureEnabled)) return;
    selected = selected.includes(tool.name) ? selected.filter(n => n !== tool.name) : [...selected, tool.name];
    onchange?.();
  }
</script>

<div class="space-y-2">
  <label class="block">
    <span class="sr-only">Search built-in tools</span>
    <input type="search" bind:value={search} placeholder="Search built-in tools" class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-surface px-3 py-1.5 text-sm text-gray-900 dark:text-dark-text" />
  </label>
  <p class="text-xs text-gray-500 dark:text-dark-text-muted">Select individual tools. Installation settings and workspace execution permissions always apply.</p>
  {#each builtinFamilies as family}
    {@const entries = tools.filter(t => builtinFamily(t) === family.key && `${t.name} ${t.description}`.toLowerCase().includes(search.toLowerCase()))}
    {#if entries.length > 0}
      <details open={family.key !== 'builtin_other' || search.length > 0} class="border border-gray-200 dark:border-dark-border">
        <summary class="cursor-pointer bg-gray-50 dark:bg-dark-base px-3 py-2 text-xs font-medium text-gray-700 dark:text-dark-text-secondary">
          {family.label} · {entries.filter(t => selected.includes(t.name) || inherited.includes(t.name)).length} selected
          {#if !isFeatureEnabled('builtin_tools') || !isFeatureEnabled(family.key)} · Disabled by administrator{/if}
        </summary>
        <div class="space-y-3 p-3">
          {#each [...new Set(entries.map(t => t.group || 'helpers'))] as group}
            <div>
              {#if family.key === 'builtin_other'}<p class="mb-1 text-xs font-medium text-gray-600 dark:text-dark-text-secondary">{builtinGroupLabel(group)}</p>{/if}
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-2 xl:grid-cols-3">
                {#each entries.filter(t => (t.group || 'helpers') === group) as tool}
                  {@const blocked = builtinDisabledBy(tool, isFeatureEnabled)}
                  <label class="flex min-w-0 items-start gap-2 text-xs text-gray-700 dark:text-dark-text-secondary" title={tool.description}>
                    <input type="checkbox" checked={selected.includes(tool.name)} disabled={!!blocked} onchange={() => toggle(tool)} class="mt-0.5 shrink-0 accent-gray-900 dark:accent-accent" />
                    <span class="min-w-0 break-words">
                      {tool.name}
                      {#if inherited.includes(tool.name)}<span class="text-purple-600 dark:text-purple-400"> · Agent</span>{/if}
                      {#if blocked}<span class="block text-gray-500 dark:text-dark-text-muted">Disabled by administrator · {builtinGroupLabel(blocked)}</span>{/if}
                    </span>
                  </label>
                {/each}
              </div>
            </div>
          {/each}
        </div>
      </details>
    {/if}
  {/each}
  {#if tools.length > 0 && !tools.some(t => `${t.name} ${t.description}`.toLowerCase().includes(search.toLowerCase()))}
    <p class="text-xs text-gray-500 dark:text-dark-text-muted">No tools match your search.</p>
  {/if}
  {#if tools.length === 0}<p class="text-xs text-gray-500 dark:text-dark-text-muted">No built-in tools are available.</p>{/if}
  {#if missing.length > 0}
    <p class="break-words text-xs text-gray-500 dark:text-dark-text-muted">Saved selections currently unavailable: {missing.join(', ')}. They will not be offered to the model.</p>
  {/if}
</div>
