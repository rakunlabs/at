<script lang="ts">
  import CodeExpander from './CodeExpander.svelte';

  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Inputs</span>
  <input
    type="number"
    bind:value={data.input_count}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
    min="1"
    max="10"
    placeholder="1"
  /></label>
  <div class="mt-0.5 text-[10px] text-dark-text-muted">
    {#if (data.input_count || 1) <= 1}
      Available as <code class="font-mono bg-dark-elevated px-0.5">data</code> in JS
    {:else}
      Available as
      {#each Array(Math.min(data.input_count || 1, 10)) as _, i}
        <code class="font-mono bg-dark-elevated px-0.5">data{i + 1}</code>{i < (data.input_count || 1) - 1 ? ', ' : ''}
      {/each}
      in JS
    {/if}
  </div>
</div>
<div>
  <CodeExpander
    bind:value={data.code}
    label="Code (JS)"
    language="javascript"
    rows={6}
    placeholder={
      (data.input_count || 1) <= 1
        ? '// Access inputs via data\nconst value = data.value * 2;\nreturn { doubled: value };'
        : '// Access inputs via data1, data2, ...\nconst sum = data1.value + data2.value;\nreturn { sum: sum };'
    }
  />
  <div class="mt-0.5 text-[10px] text-dark-text-muted">Use <code class="font-mono bg-dark-elevated px-0.5">return</code> to set the result → "true" port. <code class="font-mono bg-dark-elevated px-0.5">throw</code> → "false" port (with <code class="font-mono bg-dark-elevated px-0.5">error</code> in output). "always" always fires.</div>
</div>
<div>
  <div class="text-[11px] font-medium text-dark-text-muted mb-1">Built-in Functions</div>
  <div class="px-2 py-1.5 bg-dark-surface border border-dark-border text-[10px] font-mono text-dark-text-secondary space-y-1">
    <div><span class="text-dark-text">log.info</span>(msg, key, val, ...) <span class="font-sans text-dark-text-muted">— info log</span></div>
    <div><span class="text-dark-text">log.warn</span>(msg, key, val, ...) <span class="font-sans text-dark-text-muted">— warning log</span></div>
    <div><span class="text-dark-text">log.error</span>(msg, key, val, ...) <span class="font-sans text-dark-text-muted">— error log</span></div>
    <div><span class="text-dark-text">log.debug</span>(msg, key, val, ...) <span class="font-sans text-dark-text-muted">— debug log</span></div>
    <div><span class="text-dark-text">toString</span>(v) <span class="font-sans text-dark-text-muted">— bytes/value to string</span></div>
    <div><span class="text-dark-text">jsonParse</span>(v) <span class="font-sans text-dark-text-muted">— parse string/bytes as JSON</span></div>
    <div><span class="text-dark-text">JSON_stringify</span>(v) <span class="font-sans text-dark-text-muted">— marshal value to JSON string</span></div>
    <div><span class="text-dark-text">btoa</span>(v) <span class="font-sans text-dark-text-muted">— base64 encode</span></div>
    <div><span class="text-dark-text">atob</span>(s) <span class="font-sans text-dark-text-muted">— base64 decode</span></div>
    <div><span class="text-dark-text">getVar</span>(key) <span class="font-sans text-dark-text-muted">— read workflow variable</span></div>
    <div><span class="text-dark-text">httpGet</span>(url, headers?) <span class="font-sans text-dark-text-muted">— HTTP GET</span></div>
    <div><span class="text-dark-text">httpPost</span>(url, body?, headers?) <span class="font-sans text-dark-text-muted">— HTTP POST</span></div>
    <div><span class="text-dark-text">httpPut</span>(url, body?, headers?) <span class="font-sans text-dark-text-muted">— HTTP PUT</span></div>
    <div><span class="text-dark-text">httpDelete</span>(url, headers?) <span class="font-sans text-dark-text-muted">— HTTP DELETE</span></div>
  </div>
  <div class="mt-1 text-[10px] text-dark-text-muted">HTTP functions return <code class="font-mono bg-dark-elevated px-0.5">{"{ status, headers, body }"}</code>. Body has <code class="font-mono bg-dark-elevated px-0.5">.toString()</code>, <code class="font-mono bg-dark-elevated px-0.5">.jsonParse()</code> methods.</div>
</div>
<!-- Port descriptions -->
<div class="border-t border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Upstream data. Available as 'data' (single input) or 'data1', 'data2', etc. (multiple inputs) in JS.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">data</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Upstream data (or data1..dataN for multi-input)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">any</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Activated when the script returns successfully. Output includes 'result' field with the return value.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">true</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Script returned successfully (result in output)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ ...inputs, result: any }"}</div>
      </div>
      <div title="Activated when the script throws an error. Output includes 'error' field.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">false</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Script threw an error (error in output)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ ...inputs, result: null, error: string }"}</div>
      </div>
      <div title="Always activated regardless of success or failure.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">always</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Fires on both success and error</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ ...inputs, result: any }"}</div>
      </div>
      <div title="Only the returned value, without the inputs. Activated when the script returns. Use it to feed a prompt or template, e.g. after combining several HTTP responses.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">result</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Just the returned value</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">any</div>
      </div>
    </div>
  </div>
</div>
