<script lang="ts">
  import { untrack } from 'svelte';
  import { Plus, X } from 'lucide-svelte';
  import { outputFieldProblem } from '@/lib/workflow/output-fields';

  let { data }: { data: Record<string, any> } = $props();

  // Older graphs stored plain strings here; keep them editable as names.
  untrack(() => {
    if (!Array.isArray(data.fields)) data.fields = [];
    data.fields = data.fields.map((f: any) => (typeof f === 'string' ? f : typeof f?.name === 'string' ? f.name : '')).filter((f: string) => f !== '');
  });

  let mode = $derived(data.response_mode || 'json');

  function addField() {
    data.fields = [...data.fields, ''];
  }
  function removeField(index: number) {
    data.fields = data.fields.filter((_: string, i: number) => i !== index);
  }
</script>

<div>
  <span class="text-[11px] font-medium text-dark-text-muted">Named fields</span>
  <div class="mt-0.5 space-y-1">
    {#each data.fields as _, i}
      <div class="flex items-center gap-1">
        <input
          type="text"
          bind:value={data.fields[i]}
          class="w-full px-2 py-1 text-xs border border-dark-border-subtle font-mono focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
          placeholder="e.g. text, file"
          aria-label="Field name"
        />
        <button type="button" onclick={() => removeField(i)} class="p-1 text-dark-text-muted hover:text-dark-text-secondary" aria-label="Remove field"><X size={12} /></button>
      </div>
      {#if outputFieldProblem(data.fields[i])}
        <div class="text-[10px] text-amber-600">{outputFieldProblem(data.fields[i])} — no port is added</div>
      {/if}
    {/each}
    <button type="button" onclick={addField} class="flex items-center gap-1 px-2 py-1 text-[10px] border border-dark-border-subtle text-dark-text-secondary hover:bg-dark-surface"><Plus size={12} />Add field</button>
  </div>
  <div class="mt-0.5 text-[10px] text-dark-text-muted">Each field adds an input port and becomes its own key in the workflow outputs, so a Workflow Call can wire <span class="font-mono">text</span> and <span class="font-mono">file</span> to different steps.</div>
</div>

<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">HTTP response (sync runs)</span>
    <select
      bind:value={data.response_mode}
      class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
    >
      <option value="json">JSON envelope</option>
      <option value="file">File download</option>
      <option value="multipart">Multipart: JSON + files</option>
    </select>
  </label>
  <div class="mt-0.5 text-[10px] text-dark-text-muted">
    Applies to webhooks and <span class="font-mono">POST /api/v1/workflows/run/&lt;id&gt;</span> called with <span class="font-mono">?sync=true</span>.
    {#if mode === 'file'}The first file is returned as the body with its own Content-Type.{/if}
    {#if mode === 'multipart'}The response is <span class="font-mono">multipart/mixed</span>: a <span class="font-mono">result</span> JSON part, then one part per file.{/if}
  </div>
</div>

{#if mode !== 'json'}
  <div>
    <label class="block">
      <span class="text-[11px] font-medium text-dark-text-muted">File path (JSON Pointer)</span>
      <input
        type="text"
        bind:value={data.file_path}
        class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle font-mono focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
        placeholder="/file  or  /input/file"
      />
    </label>
    <div class="mt-0.5 text-[10px] text-dark-text-muted">Empty: every file reference found in the inputs (HTTP Request <span class="font-mono">file</span>, Agent Call <span class="font-mono">files</span>, Script <span class="font-mono">{'{name, content_base64}'}</span>).</div>
  </div>
  {#if mode === 'file'}
    <div>
      <label class="block">
        <span class="text-[11px] font-medium text-dark-text-muted">Disposition</span>
        <select
          bind:value={data.disposition}
          class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
        >
          <option value="attachment">attachment (download)</option>
          <option value="inline">inline (display)</option>
        </select>
      </label>
    </div>
  {/if}
{/if}

<!-- Port descriptions -->
<div class="border-t border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="All incoming data from upstream nodes, merged into a single map. The first output node to fire sends its result for synchronous API responses.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">input</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— All upstream data merged (workflow result)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">map — all upstream keys pass through to workflow result</div>
      </div>
      <div>
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">&lt;field&gt;</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— One port per named field; stored under that name</span>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="This is a terminal/sink node with no outputs.">
        <span class="text-[11px] text-dark-text-muted italic">None — terminal sink node</span>
      </div>
    </div>
  </div>
</div>
