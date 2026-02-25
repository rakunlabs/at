<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';

  interface HttpTriggerData {
    label?: string;
    trigger_id?: string;
  }

  let { id, data, selected }: NodeProps<HttpTriggerData> = $props();
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-44 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-indigo-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-indigo-500 text-white tracking-wide">HTTP</span>
    <span class="text-gray-900">{data.label || 'HTTP Trigger'}</span>
  </div>
  <div class="px-2.5 py-1.5">
    {#if data.trigger_id}
      <div class="flex gap-1 items-baseline mb-0.5">
        <span class="text-gray-400 text-[10px] shrink-0">Webhook:</span>
        <span class="text-gray-600 font-mono text-[10px] break-all">/api/v1/webhooks/{data.trigger_id}</span>
      </div>
    {:else}
      <div class="text-gray-400 text-[11px]">Save to generate webhook URL</div>
    {/if}
  </div>
  <Handle id="output" type="output" port="data" position="right" label="out" />
</div>
