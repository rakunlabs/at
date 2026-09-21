<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';
  import NodePreview from './NodePreview.svelte';
  import { workflowRun } from '@/lib/store/workflow-run.svelte';
  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
</script>

<div class={['workflow-node-card', selected && 'border-blue-500 ring-2 ring-blue-500/25']}>
  <Handle id="data_in" type="input" port="data" accept={['data', 'text']} position="left" label="data" />
  <div class="flex items-center gap-2 border-b border-gray-200 px-3 py-3">
    <span class="bg-amber-700 text-xs font-bold text-white">WAIT</span>
    <span class="text-gray-900">{data.label || 'Wait / Approval'}</span>
    {#if data.node_number != null}<span class="ml-auto text-xs text-gray-500">#{data.node_number}</span>{/if}
  </div>
  <div class="px-3 py-3 text-xs text-gray-600 dark:text-dark-text-secondary">
    {data.mode === 'approval' ? 'Approval required' : `Wait ${data.seconds ?? 60} seconds`}
    {#if data.mode === 'approval' && data.prompt}<p class="mt-1 line-clamp-2">{data.prompt}</p>{/if}
  </div>
  <NodePreview state={workflowRun.nodeRunStates[id]} nodeId={id} />
  <Handle id="data" type="output" port="data" position="right" label="data" />
</div>
