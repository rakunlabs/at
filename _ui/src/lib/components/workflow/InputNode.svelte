<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';
  import NodePreview from './NodePreview.svelte';
  import { workflowRun } from '@/lib/store/workflow-run.svelte';

  interface InputField {
    name: string;
    type?: string;
    description?: string;
    default?: any;
  }

  interface InputData {
    label?: string;
    fields?: InputField[];
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<InputData> = $props();
  let runState = $derived(workflowRun.nodeRunStates[id]);
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-40 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-emerald-50">
    <span class="inline-flex items-center leading-none text-[9px] font-bold px-1 py-1 rounded bg-emerald-500 text-white tracking-wide">IN</span>
    <span class="text-gray-900">{data.label || 'Input'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.fields && data.fields.length > 0}
      <div class="flex flex-col gap-0.5">
        {#each data.fields as field}
          <div class="flex items-center gap-1 text-[11px]">
            <span class="text-gray-600 font-mono">{field.name}</span>
            <span class="text-gray-300">·</span>
            <span class="text-gray-400">{field.type || 'string'}</span>
          </div>
        {/each}
      </div>
    {:else}
      <div class="text-gray-400 text-[11px]">Workflow input data</div>
    {/if}
  </div>
  <NodePreview state={runState} />
  <Handle id="output" type="output" port="data" position="right" label="out" />
</div>
