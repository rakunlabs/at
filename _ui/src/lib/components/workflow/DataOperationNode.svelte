<script lang="ts">
  import { Handle, HandleGroup, getFlow, type NodeProps } from 'kaykay';
  import { workflowRun } from '@/lib/store/workflow-run.svelte';
  import { getWorkflowNodeDefinition } from '@/lib/workflow/node-definitions';
  import { switchOutputPorts } from '@/lib/workflow/data-operations';
  import { canvasInputHandle } from '@/lib/workflow/ports';
  import NodePreview from './NodePreview.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  const flow = getFlow();
  let type = $derived(flow.getNode(id)?.type || 'edit_fields');
  let definition = $derived(getWorkflowNodeDefinition(type));
  let outputs = $derived(type === 'switch' ? switchOutputPorts(data) : [{ id: 'data', label: 'data' }]);
  let badge = $derived(({ edit_fields: 'EDIT', filter: 'FILTER', switch: 'SWITCH', merge: 'MERGE', aggregate: 'AGG' } as Record<string, string>)[type]);
  let fieldCount = $derived(Array.isArray(data.fields) ? data.fields.length : 0);
  let conditionCount = $derived(Array.isArray(data.conditions) ? data.conditions.length : 0);
  let caseCount = $derived(Array.isArray(data.rules) ? data.rules.length : 0);
</script>

<div class={['workflow-node-card', selected && 'border-blue-500 ring-2 ring-blue-500/25']} style:min-height={type === 'switch' ? `${Math.max(100, outputs.length * 24 + 20)}px` : undefined}>
  <HandleGroup position="left" class="!gap-2">
    {#if type === 'merge'}
      <Handle id="left" type="input" port="data" accept={['data', 'text']} label="left" />
      <Handle id="right" type="input" port="data" accept={['data', 'text']} label="right" />
    {:else}
      <Handle id={canvasInputHandle(type, 'data')} type="input" port="data" accept={['data', 'text']} label="data" />
    {/if}
  </HandleGroup>
  <div class="flex items-center gap-2 border-b border-gray-200 px-3 py-3">
    <span class="bg-teal-700 text-xs font-bold text-white">{badge}</span>
    <span class="text-gray-900">{data.label || definition?.label}</span>
    {#if data.node_number != null}<span class="ml-auto text-xs text-gray-500">#{data.node_number}</span>{/if}
  </div>
  <div class="px-3 py-3 text-xs text-gray-600 dark:text-dark-text-secondary">
    {#if type === 'edit_fields'}{fieldCount} {fieldCount === 1 ? 'field' : 'fields'} · {data.keep_input !== false ? 'keep other fields' : 'selected fields only'}
    {:else if type === 'filter'}{conditionCount} {conditionCount === 1 ? 'condition' : 'conditions'} · {data.match || 'all'} must match
    {:else if type === 'switch'}{caseCount} {caseCount === 1 ? 'case' : 'cases'} · {data.match_mode || 'first'} match
    {:else if type === 'merge'}{data.mode || 'append'}{data.mode === 'join' ? ` · ${data.join_type || 'inner'}` : ''}
    {:else}{data.operation || 'collect'} · {data.field_path || 'whole items'}{/if}
  </div>
  <NodePreview state={workflowRun.nodeRunStates[id]} nodeId={id} />
  <HandleGroup position="right" class="!gap-2">
    {#each outputs as port (port.id)}<Handle id={port.id} type="output" port="data" label={port.label} />{/each}
  </HandleGroup>
</div>
