<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';
  import NodePreview from './NodePreview.svelte';
  import { nodeRunStates } from '@/lib/store/workflow-run.svelte';

  interface AgentConfigData {
    label?: string;
    agent_id?: string;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<AgentConfigData> = $props();
  let runState = $derived(nodeRunStates[id]);
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-36 max-w-52 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-blue-50">
    <span class="inline-flex items-center leading-none text-[9px] font-bold px-1 py-1 rounded bg-blue-600 text-white tracking-wide">AGENT</span>
    <span class="text-gray-900">{data.label || 'Agent Config'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.agent_id}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">•</span>
        <span class="text-gray-700 font-mono text-[11px] truncate">{data.agent_id}</span>
      </div>
    {:else}
      <div class="text-gray-400 text-[11px] italic">No agent selected</div>
    {/if}
  </div>
  <NodePreview state={runState} />
  <HandleGroup position="top" class="!gap-1">
    <Handle id="agent" type="output" port="config" label="agent" />
  </HandleGroup>
</div>
