<script lang="ts">
  import { RefreshCw } from 'lucide-svelte';
  import { outputFieldNames, workflowOutputFields } from '@/lib/workflow/output-fields';

  let {
    data,
    allWorkflows = [],
    workflow = null,
  }: {
    data: Record<string, any>;
    allWorkflows?: any[];
    workflow?: any;
  } = $props();

  let exposed = $derived(outputFieldNames(data.output_fields));
  let target = $derived(allWorkflows.find(w => w.id === data.workflow_id));
  let available = $derived(workflowOutputFields(target?.graph));
  let stale = $derived(!!target && (available.length !== exposed.length || available.some((f, i) => f !== exposed[i])));

  function syncFields() {
    data.output_fields = [...available];
  }
</script>

<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Target Workflow</span>
  <select
    bind:value={data.workflow_id}
    onchange={(e) => {
      const id = (e.target as HTMLSelectElement).value;
      const wf = allWorkflows.find(w => w.id === id);
      if (wf) {
        data.workflow_name = wf.name;
      }
      // Each named field of the child's Output node becomes an output port.
      data.output_fields = workflowOutputFields(wf?.graph);
    }}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
  >
    <option value="">Select workflow</option>
    {#each allWorkflows.filter(w => w.id !== workflow?.id) as w}
      <option value={w.id}>{w.name}</option>
    {/each}
  </select></label>
</div>
<div>
  <span class="text-[11px] font-medium text-dark-text-muted">Output fields</span>
  <div class="mt-0.5 flex flex-wrap items-center gap-1">
    {#each exposed as field}
      <span class="px-1.5 py-0.5 text-[10px] font-mono border border-dark-border text-dark-text-secondary">{field}</span>
    {:else}
      <span class="text-[10px] text-dark-text-muted italic">None — the child's Output node has no named fields</span>
    {/each}
    {#if stale}
      <button type="button" onclick={syncFields} class="flex items-center gap-1 px-1.5 py-0.5 text-[10px] border border-amber-900/60 text-amber-300 hover:bg-amber-900/20"><RefreshCw size={10} />Update from child</button>
    {/if}
  </div>
  <div class="mt-0.5 text-[10px] text-dark-text-muted">Each field is a separate output port. Add named fields (e.g. <span class="font-mono">text</span>, <span class="font-mono">file</span>) on the child workflow's Output node.</div>
</div>
<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Static Inputs (JSON)</span>
  <textarea
    value={JSON.stringify(data.inputs || {}, null, 2)}
    oninput={(e) => { try { data.inputs = JSON.parse((e.target as HTMLTextAreaElement).value); } catch {} }}
    rows={4}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle font-mono focus:outline-none focus:ring-1 focus:ring-dark-border-subtle resize-y"
    placeholder={'{"key": "value"}'}
  ></textarea></label>
  <div class="mt-0.5 text-[10px] text-dark-text-muted">Merged with dynamic inputs</div>
</div>
<!-- Port descriptions -->
<div class="border-t border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Dynamic inputs merged on top of static config inputs. Static values are overridden by dynamic ones.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">inputs</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Dynamic inputs (override static config)</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">map</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="The outputs collected from the called workflow's output node(s).">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">output</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Outputs from the called workflow</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">map — shape defined by child workflow</div>
      </div>
      <div>
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">files</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Every file reference in the child's outputs</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">list — connect to Email attachments, Agent Call attachments, Output</div>
      </div>
      <div>
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">&lt;field&gt;</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— One port per output field above</span>
      </div>
    </div>
  </div>
</div>
