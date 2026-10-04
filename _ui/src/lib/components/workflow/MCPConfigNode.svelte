<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let sets = $derived<string[]>(Array.isArray(data.mcp_sets) ? data.mcp_sets : []);
  let legacyURLs = $derived<string[]>(Array.isArray(data.mcp_urls) ? data.mcp_urls : []);
</script>

<WorkflowNode {id} type="mcp_config" {data} {selected} setup={sets.length || legacyURLs.length ? '' : 'Select MCP sets'}>
  {#snippet extra()}<Handle id="mcp_urls" type="output" port="config" position="top" label="mcp" />{/snippet}
  {#if sets.length || legacyURLs.length}
    <ul class="space-y-0.5 text-xs">
      {#each sets as name}<li class="truncate font-mono text-gray-700 dark:text-dark-text-secondary" title={name}>{name}</li>{/each}
      {#if legacyURLs.length}
        <li class="truncate text-amber-700 dark:text-amber-400" title={legacyURLs.join('\n')}>{legacyURLs.length} legacy URL{legacyURLs.length === 1 ? '' : 's'}</li>
      {/if}
    </ul>
  {/if}
</WorkflowNode>
