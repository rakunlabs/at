<script lang="ts">
  let { data, nodeConfigs = [] }: { data: Record<string, any>; nodeConfigs?: any[] } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">SMTP Config</span>
  <select
    bind:value={data.config_id}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
  >
    <option value="">Select config</option>
    {#each nodeConfigs as nc}
      <option value={nc.id}>{nc.name}</option>
    {/each}
  </select></label>
  <div class="mt-0.5 text-[10px] text-gray-400">Configure SMTP servers in Node Configs</div>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">To (Go template)</span>
  <input
    type="text"
    bind:value={data.to}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder={'user@example.com, \x7B\x7B.email\x7D\x7D'}
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">CC (Go template)</span>
  <input
    type="text"
    bind:value={data.cc}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="cc@example.com"
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">BCC (Go template)</span>
  <input
    type="text"
    bind:value={data.bcc}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="bcc@example.com"
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Subject (Go template)</span>
  <input
    type="text"
    bind:value={data.subject}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder={'Alert: \x7B\x7B.title\x7D\x7D'}
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Body (Go template)</span>
  <textarea
    bind:value={data.body}
    rows={4}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder={'Hello \x7B\x7B.name\x7D\x7D,\n\nYour report is ready.'}
  ></textarea></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Content Type</span>
  <select
    bind:value={data.content_type}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
  >
    <option value="text/plain">text/plain</option>
    <option value="text/html">text/html</option>
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">From Override (Go template)</span>
  <input
    type="text"
    bind:value={data.from}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="(uses config default)"
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Reply-To (Go template)</span>
  <input
    type="text"
    bind:value={data.reply_to}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="reply@example.com"
  /></label>
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Upstream data available as template context for all address and body fields.">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— Template context for email fields</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">map</div>
      </div>
      <div title="Additional template variables merged on top of data (higher precedence).">
        <span class="text-[11px] font-mono font-medium text-gray-700">values</span>
        <span class="text-[10px] text-gray-400 ml-1">— Extra template vars (override data keys)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">map</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Activated on successful send. Output includes status='sent'.">
        <span class="text-[11px] font-mono font-medium text-gray-700">success</span>
        <span class="text-[10px] text-gray-400 ml-1">— Email sent successfully (status='sent')</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ status: \"sent\" }"}</div>
      </div>
      <div title="Activated on send failure. Output includes status='failed' and error message.">
        <span class="text-[11px] font-mono font-medium text-gray-700">error</span>
        <span class="text-[10px] text-gray-400 ml-1">— Send failed (status='failed', error message)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ status: \"failed\", error: string }"}</div>
      </div>
      <div title="Always activated regardless of success or failure.">
        <span class="text-[11px] font-mono font-medium text-gray-700">always</span>
        <span class="text-[10px] text-gray-400 ml-1">— Fires on both success and failure</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ status: string, error?: string }"}</div>
      </div>
    </div>
  </div>
</div>
