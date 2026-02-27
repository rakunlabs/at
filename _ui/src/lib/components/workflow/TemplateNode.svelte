<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';

  interface TemplateData {
    label?: string;
    template?: string;
    variables?: string[];
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<TemplateData> = $props();

  let previewText = $derived(() => {
    if (!data.template) return '';
    const maxLen = 80;
    return data.template.length > maxLen ? data.template.slice(0, maxLen) + '...' : data.template;
  });
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-45 max-w-60 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <Handle id="input" type="input" port="data" accept={['data']} position="left" label="data" />
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-yellow-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-yellow-500 text-white tracking-wide">TPL</span>
    <span class="text-gray-900">{data.label || 'Template'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.template}
      <div class="font-mono text-[10px] text-gray-500 whitespace-pre-wrap break-all leading-snug mb-1">{previewText()}</div>
    {:else}
      <div class="text-gray-400 text-[11px]">Configure template text</div>
    {/if}
    {#if data.variables && data.variables.length > 0}
      <div class="flex flex-wrap gap-0.5 mt-1">
        {#each data.variables as v}
          <span class="font-mono text-[10px] bg-yellow-100 text-yellow-800 px-1 rounded border border-yellow-300">{`{{.${v}}}`}</span>
        {/each}
      </div>
    {/if}
  </div>
  <Handle id="output" type="output" port="text" position="right" label="text" />
</div>
