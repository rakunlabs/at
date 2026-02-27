<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';

  interface ScriptData {
    label?: string;
    code?: string;
    input_count?: number;
  }

  let { id, data, selected }: NodeProps<ScriptData> = $props();

  let inputCount = $derived(Math.max(1, Math.min(data.input_count || 1, 10)));

  let previewCode = $derived(() => {
    if (!data.code) return '';
    const maxLen = 80;
    return data.code.length > maxLen ? data.code.slice(0, maxLen) + '...' : data.code;
  });
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-45 max-w-60 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  {#if inputCount === 1}
    <Handle id="data" type="input" port="data" accept={['data', 'text']} position="left" label="data" />
  {:else}
    <HandleGroup position="left" class="!gap-1">
      {#each Array(inputCount) as _, i}
        <Handle id="data{i + 1}" type="input" port="data" accept={['data', 'text']} label="data{i + 1}" />
      {/each}
    </HandleGroup>
  {/if}
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-slate-100">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-slate-700 text-white tracking-wide">JS</span>
    <span class="text-gray-900">{data.label || 'Script'}</span>
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.code}
      <div class="font-mono text-[10px] text-gray-500 whitespace-pre-wrap break-all leading-snug">{previewCode()}</div>
    {:else}
      <div class="text-gray-400 text-[11px]">Write JavaScript code</div>
    {/if}
    {#if inputCount > 1}
      <div class="text-[9px] text-gray-400 mt-0.5">{inputCount} inputs</div>
    {/if}
    <div class="text-[9px] text-gray-400 mt-1 border-t border-gray-100 pt-1">
      <code class="font-mono bg-gray-50 px-0.5 rounded">return</code> → true, <code class="font-mono bg-gray-50 px-0.5 rounded">throw</code> → false
    </div>
  </div>
  <HandleGroup position="right" class="!gap-1">
    <Handle id="true" type="output" port="data" label="true" />
    <Handle id="false" type="output" port="data" label="false" />
    <Handle id="always" type="output" port="data" label="always" />
  </HandleGroup>
</div>
