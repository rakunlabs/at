<script lang="ts">
  import { ChevronDown, X } from 'lucide-svelte';

  /**
   * Option shape: either a plain string (value == label) or an object with
   * distinct value/label for id-based selects (e.g. agent.id / agent.name).
   */
  type Option = string | { value: string; label: string };

  interface Props {
    label: string;
    options: Option[];
    selected?: string[];
    onchange?: (values: string[]) => void;
  }

  let { label, options, selected = $bindable([]), onchange }: Props = $props();

  let open = $state(false);
  let query = $state('');
  let container: HTMLDivElement;

  const normalized = $derived(
    options.map((o) => (typeof o === 'string' ? { value: o, label: o } : o)),
  );

  const filtered = $derived(
    query.trim()
      ? normalized.filter(
          (o) =>
            o.label.toLowerCase().includes(query.toLowerCase()) ||
            o.value.toLowerCase().includes(query.toLowerCase()),
        )
      : normalized,
  );

  function labelFor(value: string): string {
    return normalized.find((o) => o.value === value)?.label || value;
  }

  function toggle(value: string) {
    if (selected.includes(value)) {
      selected = selected.filter((v) => v !== value);
    } else {
      selected = [...selected, value];
    }
    onchange?.(selected);
  }

  function clearAll(e: MouseEvent) {
    e.stopPropagation();
    selected = [];
    onchange?.(selected);
  }

  // Close on outside click.
  $effect(() => {
    function onDocClick(e: MouseEvent) {
      if (!container) return;
      if (open && !container.contains(e.target as Node)) {
        open = false;
      }
    }
    document.addEventListener('click', onDocClick);
    return () => document.removeEventListener('click', onDocClick);
  });
</script>

<div bind:this={container} class="relative">
  <button
    onclick={() => (open = !open)}
    class="flex items-center gap-1.5 px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-surface text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated"
  >
    <span>{label}</span>
    {#if selected.length > 0}
      <span class="px-1 bg-gray-900 text-white dark:bg-accent text-[10px]">{selected.length}</span>
      <span
        role="button"
        tabindex="0"
        onclick={clearAll}
        onkeydown={(e) => e.key === 'Enter' && clearAll(e as any)}
        class="hover:text-red-500"
      >
        <X size={12} />
      </span>
    {/if}
    <ChevronDown size={12} />
  </button>

  {#if open}
    <div
      class="absolute z-20 mt-1 min-w-52 max-h-72 overflow-hidden bg-white dark:bg-dark-surface border border-gray-300 dark:border-dark-border-subtle shadow-lg text-xs flex flex-col"
    >
      {#if normalized.length > 8}
        <input
          type="text"
          bind:value={query}
          placeholder="Filter..."
          class="px-2 py-1 border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface focus:outline-none text-gray-700 dark:text-dark-text-secondary"
        />
      {/if}
      <div class="overflow-auto">
        {#each filtered as opt}
          <label class="flex items-center gap-2 px-2 py-1 hover:bg-gray-50 dark:hover:bg-dark-elevated cursor-pointer">
            <input
              type="checkbox"
              checked={selected.includes(opt.value)}
              onchange={() => toggle(opt.value)}
              class="accent-gray-900 dark:accent-accent"
            />
            <span class="text-gray-700 dark:text-dark-text-secondary truncate" title={opt.value}>
              {opt.label}
            </span>
          </label>
        {:else}
          <div class="px-2 py-1 text-gray-400 dark:text-dark-text-muted">
            {query ? 'No matches' : 'No options'}
          </div>
        {/each}
      </div>
    </div>
  {/if}

  <!-- Selected preview shown as title attribute on the button; when many items are selected
       users still see the count badge above. Keep the list accessible for screen readers. -->
  {#if selected.length > 0}
    <span class="sr-only">
      Selected: {selected.map(labelFor).join(', ')}
    </span>
  {/if}
</div>
