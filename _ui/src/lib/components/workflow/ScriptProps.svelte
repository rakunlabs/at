<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Inputs</span>
  <input
    type="number"
    bind:value={data.input_count}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    min="1"
    max="10"
    placeholder="1"
  /></label>
  <div class="mt-0.5 text-[10px] text-gray-400">
    {#if (data.input_count || 1) <= 1}
      Available as <code class="font-mono bg-gray-100 px-0.5 rounded">data</code> in JS
    {:else}
      Available as
      {#each Array(Math.min(data.input_count || 1, 10)) as _, i}
        <code class="font-mono bg-gray-100 px-0.5 rounded">data{i + 1}</code>{i < (data.input_count || 1) - 1 ? ', ' : ''}
      {/each}
      in JS
    {/if}
  </div>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Code (JS)</span>
  <textarea
    bind:value={data.code}
    rows={6}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder={
      (data.input_count || 1) <= 1
        ? '// Access inputs via data\nconst value = data.value * 2;\nreturn { doubled: value };'
        : '// Access inputs via data1, data2, ...\nconst sum = data1.value + data2.value;\nreturn { sum: sum };'
    }
  ></textarea></label>
  <div class="mt-0.5 text-[10px] text-gray-400">Use <code class="font-mono bg-gray-100 px-0.5 rounded">return</code> to set the result → "true" port. <code class="font-mono bg-gray-100 px-0.5 rounded">throw</code> → "false" port (with <code class="font-mono bg-gray-100 px-0.5 rounded">error</code> in output). "always" always fires.</div>
</div>
<div>
  <div class="text-[10px] font-medium text-gray-500 uppercase tracking-wider mb-1">Built-in Functions</div>
  <div class="px-2 py-1.5 bg-gray-50 border border-gray-200 rounded text-[10px] font-mono text-gray-600 space-y-1">
    <div><span class="text-gray-800">log.info</span>(msg, key, val, ...) <span class="font-sans text-gray-400">— info log</span></div>
    <div><span class="text-gray-800">log.warn</span>(msg, key, val, ...) <span class="font-sans text-gray-400">— warning log</span></div>
    <div><span class="text-gray-800">log.error</span>(msg, key, val, ...) <span class="font-sans text-gray-400">— error log</span></div>
    <div><span class="text-gray-800">log.debug</span>(msg, key, val, ...) <span class="font-sans text-gray-400">— debug log</span></div>
    <div><span class="text-gray-800">toString</span>(v) <span class="font-sans text-gray-400">— bytes/value to string</span></div>
    <div><span class="text-gray-800">jsonParse</span>(v) <span class="font-sans text-gray-400">— parse string/bytes as JSON</span></div>
    <div><span class="text-gray-800">JSON_stringify</span>(v) <span class="font-sans text-gray-400">— marshal value to JSON string</span></div>
    <div><span class="text-gray-800">btoa</span>(v) <span class="font-sans text-gray-400">— base64 encode</span></div>
    <div><span class="text-gray-800">atob</span>(s) <span class="font-sans text-gray-400">— base64 decode</span></div>
    <div><span class="text-gray-800">getVar</span>(key) <span class="font-sans text-gray-400">— read workflow variable</span></div>
    <div><span class="text-gray-800">httpGet</span>(url, headers?) <span class="font-sans text-gray-400">— HTTP GET</span></div>
    <div><span class="text-gray-800">httpPost</span>(url, body?, headers?) <span class="font-sans text-gray-400">— HTTP POST</span></div>
    <div><span class="text-gray-800">httpPut</span>(url, body?, headers?) <span class="font-sans text-gray-400">— HTTP PUT</span></div>
    <div><span class="text-gray-800">httpDelete</span>(url, headers?) <span class="font-sans text-gray-400">— HTTP DELETE</span></div>
  </div>
  <div class="mt-1 text-[10px] text-gray-400">HTTP functions return <code class="font-mono bg-gray-100 px-0.5 rounded">{"{ status, headers, body }"}</code>. Body has <code class="font-mono bg-gray-100 px-0.5 rounded">.toString()</code>, <code class="font-mono bg-gray-100 px-0.5 rounded">.jsonParse()</code> methods.</div>
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Upstream data. Available as 'data' (single input) or 'data1', 'data2', etc. (multiple inputs) in JS.">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— Upstream data (or data1..dataN for multi-input)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">any</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Activated when the script returns successfully. Output includes 'result' field with the return value.">
        <span class="text-[11px] font-mono font-medium text-gray-700">true</span>
        <span class="text-[10px] text-gray-400 ml-1">— Script returned successfully (result in output)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ ...inputs, result: any }"}</div>
      </div>
      <div title="Activated when the script throws an error. Output includes 'error' field.">
        <span class="text-[11px] font-mono font-medium text-gray-700">false</span>
        <span class="text-[10px] text-gray-400 ml-1">— Script threw an error (error in output)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ ...inputs, result: null, error: string }"}</div>
      </div>
      <div title="Always activated regardless of success or failure.">
        <span class="text-[11px] font-mono font-medium text-gray-700">always</span>
        <span class="text-[10px] text-gray-400 ml-1">— Fires on both success and error</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ ...inputs, result: any }"}</div>
      </div>
    </div>
  </div>
</div>
