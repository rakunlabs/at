<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';
  import NodePreview from './NodePreview.svelte';
  import { workflowRun } from '@/lib/store/workflow-run.svelte';

  interface GitFetchData {
    label?: string;
    repo_url?: string;
    branch?: string;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<GitFetchData> = $props();
  let runState = $derived(workflowRun.nodeRunStates[id]);

  let previewRepo = $derived(() => {
    if (!data.repo_url) return '';
    const maxLen = 30;
    return data.repo_url.length > maxLen ? '...' + data.repo_url.slice(-maxLen) : data.repo_url;
  });
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-45 max-w-60 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <Handle id="data" type="input" port="data" accept={['data', 'text']} position="left" label="data" />
  
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-orange-50">
    <span class="inline-flex items-center leading-none text-[9px] font-bold px-1 py-1 rounded bg-orange-600 text-white tracking-wide">GIT</span>
    <span class="text-gray-900">{data.label || 'Git Fetch'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.repo_url}
      <div class="font-mono text-[10px] text-gray-500 whitespace-pre-wrap break-all leading-snug">{previewRepo()}</div>
      {#if data.branch}
        <div class="text-[9px] text-gray-400 mt-0.5">branch: {data.branch}</div>
      {/if}
    {:else}
      <div class="text-gray-400 text-[11px]">Clone/pull git repository</div>
    {/if}
  </div>
  <NodePreview state={runState} />
  <HandleGroup position="right" class="!gap-1">
    <Handle id="output" type="output" port="data" label="output" />
  </HandleGroup>
</div>
