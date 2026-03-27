<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';
  import NodePreview from './NodePreview.svelte';
  import { workflowRun } from '@/lib/store/workflow-run.svelte';

  interface AgentCallData {
    label?: string;
    agent_id?: string;
    provider?: string;
    model?: string;
    system_prompt?: string;
    max_iterations?: number;
    node_number?: number;
  }

  let { id, data, selected }: NodeProps<AgentCallData> = $props();
  let runState = $derived(workflowRun.nodeRunStates[id]);
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-45 max-w-60 text-xs shadow-sm select-none',
    selected && 'border-purple-500 ring-2 ring-purple-500/25'
  ]}
>
  <HandleGroup position="left" class="!gap-1">
    <Handle id="prompt" type="input" port="text" accept={['text', 'data']} label="prompt" />
    <Handle id="context" type="input" port="data" accept={['data', 'text']} label="context" />
  </HandleGroup>
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-purple-50">
    <span class="inline-flex items-center leading-none text-[9px] font-bold px-1 py-1 rounded bg-purple-500 text-white tracking-wide">AGENT</span>
    <span class="text-gray-900">{data.label || 'Agent Call'}</span>
    {#if data.node_number != null}<span class="text-[9px] font-medium text-gray-400 ml-auto">#{data.node_number}</span>{/if}
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.agent_id}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Preset:</span>
        <span class="text-gray-700 font-mono text-[11px] truncate max-w-[120px]" title={data.agent_id}>Loaded</span>
      </div>
    {/if}
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
    {#if data.max_iterations !== undefined}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Max iter:</span>
        <span class="text-gray-700 font-mono text-[11px]">{data.max_iterations === 0 ? 'unlimited' : data.max_iterations}</span>
      </div>
    {/if}
    {#if !data.provider && !data.model && !data.agent_id}
      <div class="text-gray-400 text-[11px]">Configure provider & model</div>
    {/if}
  </div>
  <NodePreview state={runState} />
  <HandleGroup position="right" class="!gap-1">
    <Handle id="response" type="output" port="text" label="response" />
  </HandleGroup>
  <HandleGroup position="bottom" class="!gap-1">
    <Handle id="skills" type="input" port="config" accept={['config']} label="skills" />
    <Handle id="mcp" type="input" port="config" accept={['config']} label="mcp" />
    <Handle id="memory" type="input" port="config" accept={['config']} label="memory" />
    <Handle id="agents" type="input" port="config" accept={['config']} label="agents" />
  </HandleGroup>
</div>
