<script lang="ts">
  import type { NodeRunState } from '@/lib/workflow/run-events';
  import NodeDataView from './NodeDataView.svelte';
  let { data, ports, state: runState, disabled = false }: {
    data: Record<string, any>;
    ports: { id: string; label?: string }[];
    state?: NodeRunState;
    disabled?: boolean;
  } = $props();
  let target = $state('');
  let path = $state('');
  let error = $state('');
  let mappings = $derived(Object.entries(data.input_mappings ?? {}) as [string, string][]);
  let selectedTarget = $derived(ports.some(port => port.id === target) ? target : ports[0]?.id ?? '');

  function mapField(pointer: string) {
    if (disabled || !selectedTarget) return;
    if (!pointer.startsWith('/') || /~(?![01])/.test(pointer)) {
      error = 'Use a JSON Pointer starting with /; escape / as ~1 and ~ as ~0 inside field names.';
      return;
    }
    data.input_mappings = { ...data.input_mappings, [selectedTarget]: pointer };
    path = pointer;
    error = '';
  }
  function removeMapping(port: string) {
    const next = { ...data.input_mappings };
    delete next[port];
    data.input_mappings = next;
  }
</script>

<p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Select a field from the connected input and assign it to an input port. Mappings read the original input independently; they do not evaluate templates or JavaScript.</p>
{#if ports.length}
  <fieldset {disabled} class="space-y-2">
    <label class="block text-xs text-gray-700 dark:text-dark-text-secondary">Target input
      <select value={selectedTarget} onchange={event => target = event.currentTarget.value} class="mt-1 w-full border border-gray-300 bg-white p-2 text-sm text-gray-900 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text">
        {#each ports as port (port.id)}<option value={port.id}>{port.label || port.id} ({port.id})</option>{/each}
      </select>
    </label>
    <label class="block text-xs text-gray-700 dark:text-dark-text-secondary">Field path (JSON Pointer)
      <input bind:value={path} placeholder="/prompt/customer/message" class="mt-1 w-full border border-gray-300 bg-white p-2 font-mono text-xs dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text" />
    </label>
    <button onclick={() => mapField(path)} disabled={!selectedTarget || !path} class="border border-gray-300 px-3 py-2 text-xs text-gray-700 disabled:opacity-50 dark:border-dark-border-subtle dark:text-dark-text">Set mapping</button>
    {#if error}<p role="alert" class="text-xs text-red-700 dark:text-red-400">{error}</p>{/if}
  </fieldset>
{/if}
{#if mappings.length}
  <div class="space-y-2 border-y border-gray-200 py-3 dark:border-dark-border">
    <h3 class="text-xs font-semibold text-gray-900 dark:text-dark-text">Configured mappings</h3>
    {#each mappings as [port, pointer] (port)}
      <div class="flex items-start justify-between gap-2 text-xs">
        <div class="min-w-0 break-all text-gray-700 dark:text-dark-text"><strong>{port}</strong> ← <code>{pointer || '(whole input)'}</code></div>
        <button {disabled} onclick={() => removeMapping(port)} aria-label={`Remove mapping for ${port}`} class="shrink-0 px-2 py-1 text-red-700 disabled:opacity-50 dark:text-red-400">Remove</button>
      </div>
    {/each}
    <p class="text-xs text-gray-600 dark:text-dark-text-secondary">Apply or Save to keep changes. Missing paths stop this step before it calls an external service.</p>
  </div>
{/if}
<h3 class="text-xs font-semibold text-gray-900 dark:text-dark-text">Connected input · last run</h3>
{#if runState?.pinned}
  <p class="text-xs text-gray-600 dark:text-dark-text-secondary">Pinned output was used. This node did not execute, so no input was captured for this invocation.</p>
{:else}
  <NodeDataView value={runState?.inputs} omitted={runState?.inputs_omitted} onselect={!disabled && selectedTarget ? mapField : undefined} />
{/if}
{#if runState?.resolved_inputs !== undefined || runState?.resolved_inputs_omitted}
  <h3 class="text-xs font-semibold text-gray-900 dark:text-dark-text">After mapping · last run</h3>
  <NodeDataView value={runState.resolved_inputs} omitted={runState.resolved_inputs_omitted} />
{/if}
