<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';
  import NodePreview from './NodePreview.svelte';
  import { workflowRun } from '@/lib/store/workflow-run.svelte';

  interface DecisionData {
    label?: string;
    provider?: string;
    model?: string;
    questions?: Record<string, any> | string;
    min_confidence?: number;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<DecisionData> = $props();
  let runState = $derived(workflowRun.nodeRunStates[id]);

  let questionIds = $derived.by(() => {
    let q = data.questions;
    if (typeof q === 'string') {
      try { q = JSON.parse(q); } catch { return []; }
    }
    return q && typeof q === 'object' ? Object.keys(q) : [];
  });
</script>

<div
  class={[
    'workflow-node-card',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <Handle id="state" type="input" port="data" accept={['data', 'text']} position="left" label="state" />
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-violet-50">
    <span class="inline-flex items-center leading-none text-[9px] font-bold px-1 py-1 rounded bg-violet-600 text-white tracking-wide">S1</span>
    <span class="text-gray-900">{data.label || 'Decision'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.provider}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Provider:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.provider}{data.model ? `/${data.model}` : ''}</span>
      </div>
    {/if}
    {#if questionIds.length > 0}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Asks:</span>
        <span class="text-gray-700 font-mono text-[11px] overflow-hidden text-ellipsis whitespace-nowrap max-w-40 inline-block">{questionIds.join(', ')}</span>
      </div>
    {/if}
    {#if data.min_confidence}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Escalate below:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.min_confidence}</span>
      </div>
    {/if}
    {#if !data.provider}
      <div class="text-gray-400 text-[11px]">Configure a decision provider</div>
    {/if}
  </div>
  <NodePreview state={runState} nodeId={id} />
  <HandleGroup position="right" class="!gap-1">
    <Handle id="decided" type="output" port="data" label="decided" />
    <Handle id="escalate" type="output" port="data" label="escalate" />
  </HandleGroup>
</div>
