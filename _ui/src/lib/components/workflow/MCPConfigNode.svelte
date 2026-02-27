<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';

  interface MCPConfigData {
    label?: string;
    mcp_urls?: string[];
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<MCPConfigData> = $props();
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-36 max-w-52 text-xs shadow-sm select-none',
    selected && 'border-orange-500 ring-2 ring-orange-500/25'
  ]}
>
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-orange-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-orange-500 text-white tracking-wide">MCP</span>
    <span class="text-gray-900">{data.label || 'MCP Config'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.mcp_urls && data.mcp_urls.length > 0}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Servers:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.mcp_urls.length}</span>
      </div>
      {#each data.mcp_urls as url}
        <div class="flex gap-1 items-baseline mb-0.5">
          <span class="text-gray-400 text-[10px] shrink-0">•</span>
          <span class="text-gray-700 font-mono text-[10px] overflow-hidden text-ellipsis whitespace-nowrap max-w-36 inline-block">{url}</span>
        </div>
      {/each}
    {:else}
      <div class="text-gray-400 text-[11px]">No MCP servers</div>
    {/if}
  </div>
  <HandleGroup position="top" class="!gap-1">
    <Handle id="mcp_urls" type="output" port="data" label="mcp" />
  </HandleGroup>
</div>
