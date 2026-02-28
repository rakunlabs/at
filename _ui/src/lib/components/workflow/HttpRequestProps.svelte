<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">URL (Go template)</span>
  <input
    type="text"
    bind:value={data.url}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder={'https://api.example.com/\x7B\x7B.path\x7D\x7D'}
  /></label>
  <div class="mt-0.5 text-[10px] text-gray-400">Supports Go templates with data from "values" input</div>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Method (Go template)</span>
  <input
    type="text"
    bind:value={data.method}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="GET"
  /></label>
  <div class="mt-0.5 text-[10px] text-gray-400">GET, POST, PUT, PATCH, DELETE or a Go template</div>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Headers (JSON, values support templates)</span>
  <textarea
    value={JSON.stringify(data.headers || {}, null, 2)}
    oninput={(e) => { try { data.headers = JSON.parse((e.target as HTMLTextAreaElement).value); } catch {} }}
    rows={2}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder={'{"Authorization": "Bearer \x7B\x7B.token\x7D\x7D"}'}
  ></textarea></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Body (Go template)</span>
  <textarea
    bind:value={data.body}
    rows={3}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder={'{"name": "\x7B\x7B.name\x7D\x7D", "count": \x7B\x7B.count\x7D\x7D}'}
  ></textarea></label>
  <div class="mt-0.5 text-[10px] text-gray-400">Leave empty to auto-send input data as JSON for POST/PUT/PATCH</div>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Timeout (seconds)</span>
  <input
    type="number"
    bind:value={data.timeout}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="30"
    min="1"
    max="300"
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Proxy URL</span>
  <input
    type="text"
    bind:value={data.proxy}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="http://proxy.example.com:8080"
  /></label>
</div>
<div class="flex items-center gap-4">
  <label class="flex items-center gap-1.5 text-[10px] font-medium text-gray-500 uppercase tracking-wider cursor-pointer">
    <input type="checkbox" bind:checked={data.insecure_skip_verify} class="rounded border-gray-300" />
    Insecure TLS
  </label>
  <label class="flex items-center gap-1.5 text-[10px] font-medium text-gray-500 uppercase tracking-wider cursor-pointer">
    <input type="checkbox" bind:checked={data.retry} class="rounded border-gray-300" />
    Retry
  </label>
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Upstream data available as template context and used as JSON body fallback for POST/PUT/PATCH when no body template is set.">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— Template context + auto JSON body fallback</span>
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
      <div title="Activated when HTTP status is 2xx. Contains response, status_code, headers.">
        <span class="text-[11px] font-mono font-medium text-gray-700">success</span>
        <span class="text-[10px] text-gray-400 ml-1">— 2xx responses (response, status_code, headers)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ response: any, status_code: number, headers: Record<string, string> }"}</div>
      </div>
      <div title="Activated when HTTP status >= 400. Contains response, status_code, headers.">
        <span class="text-[11px] font-mono font-medium text-gray-700">error</span>
        <span class="text-[10px] text-gray-400 ml-1">— 4xx/5xx responses</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ response: any, status_code: number, headers: Record<string, string> }"}</div>
      </div>
      <div title="Always activated regardless of status code.">
        <span class="text-[11px] font-mono font-medium text-gray-700">always</span>
        <span class="text-[10px] text-gray-400 ml-1">— Fires for every response</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ response: any, status_code: number, headers: Record<string, string> }"}</div>
      </div>
    </div>
  </div>
</div>
