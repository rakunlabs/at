<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let urls = $derived<string[]>(Array.isArray(data.mcp_urls) ? data.mcp_urls : []);
</script>

<WorkflowNode {id} type="mcp_config" {data} {selected} setup={urls.length ? '' : 'Add MCP servers'}>
  {#snippet extra()}<Handle id="mcp_urls" type="output" port="config" position="top" label="mcp" />{/snippet}
  {#if urls.length}
    <ul class="space-y-0.5 text-xs">
      {#each urls as url}<li class="truncate font-mono text-gray-700 dark:text-dark-text-secondary" title={url}>{url}</li>{/each}
    </ul>
  {/if}
</WorkflowNode>
