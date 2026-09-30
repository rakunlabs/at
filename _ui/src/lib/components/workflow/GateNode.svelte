<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';
  import NodePreview from './NodePreview.svelte';
  import { workflowRun } from '@/lib/store/workflow-run.svelte';
  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
</script>

<div class={['workflow-node-card', selected && 'border-blue-500 ring-2 ring-blue-500/25']}>
  <HandleGroup position="left" class="!gap-1">
    <Handle id="data_in" type="input" port="data" accept={['data', 'text']} label="data" />
    <Handle id="signal" type="input" port="data" accept={['data', 'text']} label="signal" />
  </HandleGroup>
  <div class="flex items-center gap-2 border-b border-gray-200 px-3 py-3">
    <span class="bg-amber-600 text-xs font-bold text-white">GATE</span>
    <span class="text-gray-900">{data.label || 'Gate'}</span>
    {#if data.node_number != null}<span class="ml-auto text-xs text-gray-500">#{data.node_number}</span>{/if}
  </div>
  <div class="px-3 py-3 text-xs text-gray-600 dark:text-dark-text-secondary">
    Continues after signal{data.pass_signal ? ' · passes signal' : ''}
  </div>
  <NodePreview state={workflowRun.nodeRunStates[id]} nodeId={id} />
  <Handle id="data" type="output" port="data" position="right" label="data" />
</div>
