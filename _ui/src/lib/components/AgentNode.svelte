<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';

  interface AgentNodeData {
    label?: string;
    agent_id?: string;
    name?: string;
    role?: string;
    title?: string;
    model?: string;
    status?: string;
    is_root?: boolean;
  }

  let { id, data, selected }: NodeProps<AgentNodeData> = $props();

  const statusColors: Record<string, string> = {
    active: 'bg-green-500',
    idle: 'bg-gray-400',
    busy: 'bg-amber-500',
    offline: 'bg-red-400',
  };
</script>

<div
  class={[
    'bg-white dark:bg-gray-800 border border-gray-300 dark:border-gray-600 rounded-lg min-w-48 max-w-64 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25',
  ]}
>
  <!-- Input handle (from parent agent) — hidden for root nodes -->
  {#if !data.is_root}
    <Handle id="parent" type="input" port="data" position="top" label="" />
  {/if}

  <!-- Header -->
  <div class="flex items-center gap-2 px-3 py-2 border-b border-gray-200 dark:border-gray-700 font-medium {data.is_root ? 'bg-indigo-50 dark:bg-indigo-900/30' : 'bg-slate-50 dark:bg-gray-700/50'}">
    <span class="relative flex h-2 w-2 shrink-0">
      <span class="inline-block h-2 w-2 rounded-full {statusColors[data.status || ''] || statusColors.idle}"></span>
    </span>
    <span class="text-gray-900 dark:text-gray-100 truncate">{data.name || data.label || 'Agent'}</span>
  </div>

  <!-- Body -->
  <div class="px-3 py-2 space-y-0.5">
    {#if data.title}
      <div class="text-gray-700 dark:text-gray-300 font-medium text-[11px]">{data.title}</div>
    {/if}
    {#if data.role}
      <div class="flex gap-1 items-baseline">
        <span class="text-gray-400 dark:text-gray-500 text-[10px] shrink-0">Role:</span>
        <span class="text-gray-600 dark:text-gray-400 text-[11px] truncate">{data.role}</span>
      </div>
    {/if}
    {#if data.model}
      <div class="flex gap-1 items-baseline">
        <span class="text-gray-400 dark:text-gray-500 text-[10px] shrink-0">Model:</span>
        <span class="text-gray-600 dark:text-gray-400 font-mono text-[11px] truncate">{data.model}</span>
      </div>
    {/if}
    {#if !data.title && !data.role && !data.model}
      <div class="text-gray-400 dark:text-gray-500 text-[11px]">No details configured</div>
    {/if}
  </div>

  <!-- Output handle (to child agents) -->
  <Handle id="children" type="output" port="data" position="bottom" label="" />
</div>
