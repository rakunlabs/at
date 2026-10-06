<script lang="ts">
  import { deploymentUrl } from '@/lib/helper/deployment-url';

  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">Public</span>
  <label class="mt-0.5 flex items-center gap-1.5 cursor-pointer">
    <input
      type="checkbox"
      bind:checked={data.public}
      class="border-dark-border-subtle text-dark-text focus:ring-dark-border-subtle"
    />
    <span class="text-[10px] text-dark-text-secondary">
      {data.public ? 'No authentication required' : 'Requires Bearer token'}
    </span>
  </label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">Alias</span>
  <input
    type="text"
    bind:value={data.alias}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle font-mono focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
    placeholder="e.g. order-created"
  /></label>
  <div class="mt-0.5 text-[10px] text-dark-text-muted">Optional human-friendly URL slug</div>
</div>
<div>
  {#if data.trigger_id}
    <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">Webhook URL</span>
    <div class="mt-0.5 px-2 py-1 text-[10px] font-mono text-dark-text-secondary bg-dark-surface border border-dark-border break-all">
      {deploymentUrl(`webhooks/${data.alias || data.trigger_id}`)}
    </div>
    <div class="mt-1 text-[10px] text-dark-text-muted">
      ID: <span class="font-mono">{data.trigger_id}</span>
    </div>
    {#if !data.public}
      <div class="mt-1 px-2 py-1 bg-yellow-900/20 border border-yellow-900/60 text-[10px] text-yellow-300">
        Requires <span class="font-mono">Authorization: Bearer &lt;token&gt;</span> header
      </div>
    {/if}
  {:else}
    <div class="text-[10px] text-dark-text-muted italic">Save the workflow to generate a webhook URL</div>
  {/if}
</div>
<div>
  <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">Output Fields</span>
  <div class="mt-0.5 px-2 py-1.5 bg-dark-surface border border-dark-border text-[10px] font-mono text-dark-text-secondary space-y-0.5">
    <div><span class="text-dark-text-muted">data.</span>method <span class="text-dark-text-muted font-sans">— HTTP method</span></div>
    <div><span class="text-dark-text-muted">data.</span>path <span class="text-dark-text-muted font-sans">— request path</span></div>
    <div><span class="text-dark-text-muted">data.</span>query <span class="text-dark-text-muted font-sans">— query params (map)</span></div>
    <div><span class="text-dark-text-muted">data.</span>headers <span class="text-dark-text-muted font-sans">— request headers (map)</span></div>
    <div><span class="text-dark-text-muted">data.</span>body <span class="text-dark-text-muted font-sans">— raw body (reader)</span></div>
    <div><span class="text-dark-text-muted">data.</span>file <span class="text-dark-text-muted font-sans">— uploaded file (first), stored in the run workspace</span></div>
    <div><span class="text-dark-text-muted">data.</span>files <span class="text-dark-text-muted font-sans">— every uploaded file</span></div>
    <div><span class="text-dark-text-muted">data.</span>form <span class="text-dark-text-muted font-sans">— multipart text fields</span></div>
  </div>
  <div class="mt-1 text-[10px] text-dark-text-muted">
    multipart/form-data uploads and binary bodies (PDF, images…) become file references you can wire into Email attachments, Agent Call attachments or an Output. Add <span class="font-mono">?sync=true</span> to wait for the result; the Output node decides whether it is JSON, a file or multipart.
  </div>
  <div class="mt-1 px-2 py-1 bg-dark-surface border border-dark-border text-[10px] text-dark-text-muted">
    <div class="font-medium text-dark-text-secondary mb-0.5">Body methods:</div>
    <div class="font-mono space-y-0.5">
      <div>data.body.toString()</div>
      <div>data.body.jsonParse()</div>
      <div>data.body.toBase64()</div>
      <div>data.body.bytes()</div>
    </div>
  </div>
</div>
<!-- Port descriptions -->
<div class="border-t border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="This is a trigger/source node with no runtime inputs.">
        <span class="text-[11px] text-dark-text-muted italic">None — trigger source node</span>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-dark-text-muted uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="HTTP request data including method, path, query, headers, and body.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">data</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— HTTP request data (see output fields above)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">{"{ data: { method: string, path: string, query: map, headers: map, body: any } }"}</div>
      </div>
    </div>
  </div>
</div>
