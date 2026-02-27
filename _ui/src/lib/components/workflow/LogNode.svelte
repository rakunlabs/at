<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';

  interface LogData {
    label?: string;
    level?: string;
    message?: string;
  }

  let { id, data, selected }: NodeProps<LogData> = $props();

  const levelColors: Record<string, string> = {
    info: 'bg-blue-100 text-blue-700 border-blue-300',
    warn: 'bg-yellow-100 text-yellow-700 border-yellow-300',
    error: 'bg-red-100 text-red-700 border-red-300',
    debug: 'bg-gray-100 text-gray-600 border-gray-300',
  };

  let level = $derived(data.level || 'info');
  let levelClass = $derived(levelColors[level] || levelColors.info);

  let previewMsg = $derived(() => {
    if (!data.message) return '';
    const maxLen = 60;
    return data.message.length > maxLen ? data.message.slice(0, maxLen) + '...' : data.message;
  });
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-40 max-w-55 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <Handle id="data" type="input" port="data" accept={['data', 'text']} position="left" label="data" />
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-slate-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-slate-500 text-white tracking-wide">LOG</span>
    <span class="text-gray-900">{data.label || 'Log'}</span>
    <span class="ml-auto text-[9px] font-mono px-1 py-px rounded border {levelClass}">{level}</span>
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.message}
      <div class="font-mono text-[10px] text-gray-500 whitespace-pre-wrap break-all leading-snug">{previewMsg()}</div>
    {:else}
      <div class="text-gray-400 text-[11px]">Pass-through logger</div>
    {/if}
  </div>
  <Handle id="output" type="output" port="data" position="right" label="data" />
</div>
