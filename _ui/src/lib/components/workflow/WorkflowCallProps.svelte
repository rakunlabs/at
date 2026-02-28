<script lang="ts">
  let {
    data,
    allWorkflows = [],
    workflow = null,
  }: {
    data: Record<string, any>;
    allWorkflows?: any[];
    workflow?: any;
  } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Target Workflow</span>
  <select
    bind:value={data.workflow_id}
    onchange={(e) => {
      const id = (e.target as HTMLSelectElement).value;
      const wf = allWorkflows.find(w => w.id === id);
      if (wf) {
        data.workflow_name = wf.name;
      }
    }}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
  >
    <option value="">Select workflow</option>
    {#each allWorkflows.filter(w => w.id !== workflow?.id) as w}
      <option value={w.id}>{w.name}</option>
    {/each}
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Static Inputs (JSON)</span>
  <textarea
    value={JSON.stringify(data.inputs || {}, null, 2)}
    oninput={(e) => { try { data.inputs = JSON.parse((e.target as HTMLTextAreaElement).value); } catch {} }}
    rows={4}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder={'{"key": "value"}'}
  ></textarea></label>
  <div class="mt-0.5 text-[10px] text-gray-400">Merged with dynamic inputs</div>
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Dynamic inputs merged on top of static config inputs. Static values are overridden by dynamic ones.">
        <span class="text-[11px] font-mono font-medium text-gray-700">inputs</span>
        <span class="text-[10px] text-gray-400 ml-1">— Dynamic inputs (override static config)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">map</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="The outputs collected from the called workflow's output node(s).">
        <span class="text-[11px] font-mono font-medium text-gray-700">output</span>
        <span class="text-[10px] text-gray-400 ml-1">— Outputs from the called workflow</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">map — shape defined by child workflow</div>
      </div>
    </div>
  </div>
</div>
