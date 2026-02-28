<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Public</span>
  <label class="mt-0.5 flex items-center gap-1.5 cursor-pointer">
    <input
      type="checkbox"
      bind:checked={data.public}
      class="rounded border-gray-300 text-gray-900 focus:ring-gray-400"
    />
    <span class="text-[10px] text-gray-600">
      {data.public ? 'No authentication required' : 'Requires Bearer token'}
    </span>
  </label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Alias</span>
  <input
    type="text"
    bind:value={data.alias}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="e.g. order-created"
  /></label>
  <div class="mt-0.5 text-[10px] text-gray-400">Optional human-friendly URL slug</div>
</div>
<div>
  {#if data.trigger_id}
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Webhook URL</span>
    <div class="mt-0.5 px-2 py-1 text-[10px] font-mono text-gray-600 bg-gray-50 border border-gray-200 rounded break-all">
      /webhooks/{data.alias || data.trigger_id}
    </div>
    <div class="mt-1 text-[10px] text-gray-400">
      ID: <span class="font-mono">{data.trigger_id}</span>
    </div>
    {#if !data.public}
      <div class="mt-1 px-2 py-1 bg-yellow-50 border border-yellow-200 rounded text-[10px] text-yellow-700">
        Requires <span class="font-mono">Authorization: Bearer &lt;token&gt;</span> header
      </div>
    {/if}
  {:else}
    <div class="text-[10px] text-gray-400 italic">Save the workflow to generate a webhook URL</div>
  {/if}
</div>
<div>
  <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Fields</span>
  <div class="mt-0.5 px-2 py-1.5 bg-gray-50 border border-gray-200 rounded text-[10px] font-mono text-gray-600 space-y-0.5">
    <div><span class="text-gray-400">data.</span>method <span class="text-gray-400 font-sans">— HTTP method</span></div>
    <div><span class="text-gray-400">data.</span>path <span class="text-gray-400 font-sans">— request path</span></div>
    <div><span class="text-gray-400">data.</span>query <span class="text-gray-400 font-sans">— query params (map)</span></div>
    <div><span class="text-gray-400">data.</span>headers <span class="text-gray-400 font-sans">— request headers (map)</span></div>
    <div><span class="text-gray-400">data.</span>body <span class="text-gray-400 font-sans">— raw body (reader)</span></div>
  </div>
  <div class="mt-1 px-2 py-1 bg-gray-50 border border-gray-200 rounded text-[10px] text-gray-500">
    <div class="font-medium text-gray-600 mb-0.5">Body methods:</div>
    <div class="font-mono space-y-0.5">
      <div>data.body.toString()</div>
      <div>data.body.jsonParse()</div>
      <div>data.body.toBase64()</div>
      <div>data.body.bytes()</div>
    </div>
  </div>
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
      <div title="HTTP request data including method, path, query, headers, and body.">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— HTTP request data (see output fields above)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ data: { method: string, path: string, query: map, headers: map, body: any } }"}</div>
      </div>
    </div>
  </div>
</div>
