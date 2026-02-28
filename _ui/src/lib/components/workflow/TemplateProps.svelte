<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Template</span>
  <textarea
    bind:value={data.template}
    rows={4}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
     placeholder={'Hello \x7B\x7B.name\x7D\x7D, ...'}
  ></textarea></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Variables (comma separated)</span>
  <input
    type="text"
    value={data.variables?.join(', ') || ''}
    oninput={(e) => { data.variables = (e.target as HTMLInputElement).value.split(',').map((s: string) => s.trim()).filter(Boolean); }}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="name, topic"
  /></label>
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Upstream data used as the template context. If 'data' is the only input and is a map, its fields are promoted to top level.">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— Template context data (fields promoted to top level)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">map</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="The rendered template string output.">
        <span class="text-[11px] font-mono font-medium text-gray-700">text</span>
        <span class="text-[10px] text-gray-400 ml-1">— Rendered template string</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ text: string }"}</div>
      </div>
    </div>
  </div>
</div>
