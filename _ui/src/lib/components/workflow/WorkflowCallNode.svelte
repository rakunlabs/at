<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';

  interface WorkflowCallData {
    label?: string;
    workflow_id?: string;
    workflow_name?: string;
    inputs?: Record<string, any>;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<WorkflowCallData> = $props();
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-45 text-xs shadow-sm select-none',
    selected && 'border-indigo-500 ring-2 ring-indigo-500/25'
  ]}
>
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-indigo-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-indigo-500 text-white tracking-wide">WF</span>
    <span class="text-gray-900">{data.label || 'Workflow Call'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>

  <div class="px-2.5 py-1.5 space-y-1">
    {#if data.workflow_id}
      <div class="flex gap-1 items-baseline">
        <span class="text-gray-400 text-[10px] shrink-0">Call:</span>
        <span class="text-gray-700 font-mono text-[11px] truncate max-w-[140px]" title={data.workflow_id}>
          {data.workflow_name || data.workflow_id}
        </span>
      </div>
    {:else}
      <div class="text-gray-400 text-[11px] italic">Select a workflow...</div>
    {/if}
  </div>

  <HandleGroup position="left">
    <Handle id="inputs" type="input" port="data" label="inputs" />
  </HandleGroup>

  <HandleGroup position="right">
    <Handle id="output" type="output" port="data" label="output" />
  </HandleGroup>
</div>

