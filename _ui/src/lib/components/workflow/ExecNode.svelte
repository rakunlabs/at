<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';

  interface ExecData {
    label?: string;
    command?: string;
    working_dir?: string;
    timeout?: number;
    sandbox_root?: string;
    input_count?: number;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<ExecData> = $props();

  let inputCount = $derived(Math.max(1, Math.min(data.input_count || 1, 10)));

  let previewCmd = $derived(() => {
    if (!data.command) return '';
    const maxLen = 60;
    return data.command.length > maxLen ? data.command.slice(0, maxLen) + '...' : data.command;
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
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-orange-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-orange-600 text-white tracking-wide">SH</span>
    <span class="text-gray-900">{data.label || 'Exec'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.command}
      <div class="font-mono text-[10px] text-gray-500 whitespace-pre-wrap break-all leading-snug">{previewCmd()}</div>
    {:else}
      <div class="text-gray-400 text-[11px]">Shell command to run</div>
    {/if}
    {#if data.working_dir}
      <div class="text-[9px] text-gray-400 mt-0.5">dir: {data.working_dir}</div>
    {/if}
    {#if data.timeout && data.timeout !== 60}
      <div class="text-[9px] text-gray-400 mt-0.5">timeout: {data.timeout}s</div>
    {/if}
  </div>
  <HandleGroup position="right" class="!gap-1">
    <Handle id="true" type="output" port="data" label="ok" />
    <Handle id="false" type="output" port="data" label="fail" />
    <Handle id="always" type="output" port="data" label="always" />
  </HandleGroup>
</div>
