<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Schedule (cron)</span>
  <input
    type="text"
    bind:value={data.schedule}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="*/5 * * * *"
  /></label>
  <div class="mt-0.5 text-[10px] text-gray-400">Standard 5-field cron expression</div>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Timezone</span>
  <input
    type="text"
    bind:value={data.timezone}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="UTC"
  /></label>
  <div class="mt-0.5 text-[10px] text-gray-400">e.g. America/New_York or leave empty for local/UTC</div>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Static Payload (JSON)</span>
  <textarea
    value={JSON.stringify(data.payload || {}, null, 2)}
    oninput={(e) => { try { data.payload = JSON.parse((e.target as HTMLTextAreaElement).value); } catch {} }}
    rows={3}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder={'{"key": "value"}'}
  ></textarea></label>
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="This is a trigger/source node with no runtime inputs.">
        <span class="text-[11px] text-gray-400 italic">None — trigger source node</span>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Merged map of static payload + trigger metadata (trigger_type, triggered_at, schedule, trigger_id).">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— Static payload merged with cron metadata</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ data: { ...payload, trigger_type: string, triggered_at: string, schedule: string, trigger_id: string } }"}</div>
      </div>
    </div>
  </div>
</div>
