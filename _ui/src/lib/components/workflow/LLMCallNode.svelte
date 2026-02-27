<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';

  interface LLMCallData {
    label?: string;
    provider?: string;
    model?: string;
    system_prompt?: string;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<LLMCallData> = $props();
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-45 max-w-60 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <HandleGroup position="left" class="!gap-1">
    <Handle id="prompt" type="input" port="text" accept={['text', 'data']} label="prompt" />
    <Handle id="context" type="input" port="data" accept={['data', 'text']} label="context" />
  </HandleGroup>
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-blue-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-blue-500 text-white tracking-wide">LLM</span>
    <span class="text-gray-900">{data.label || 'LLM Call'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.provider}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Provider:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.provider}</span>
      </div>
    {/if}
    {#if data.model}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Model:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.model}</span>
      </div>
    {/if}
    {#if data.system_prompt}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">System:</span>
        <span class="text-gray-700 font-mono text-[11px] overflow-hidden text-ellipsis whitespace-nowrap max-w-32 inline-block">{data.system_prompt}</span>
      </div>
    {/if}
    {#if !data.provider && !data.model}
      <div class="text-gray-400 text-[11px]">Configure provider & model</div>
    {/if}
  </div>
  <HandleGroup position="right" class="!gap-1">
    <Handle id="response" type="output" port="data" label="response" />
  </HandleGroup>
</div>
