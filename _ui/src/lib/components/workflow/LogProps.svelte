<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Level</span>
  <select
    bind:value={data.level}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
  >
    <option value="info">info</option>
    <option value="warn">warn</option>
    <option value="error">error</option>
    <option value="debug">debug</option>
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Message (Go template)</span>
  <textarea
    bind:value={data.message}
    rows={3}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder={'Processing \x7B\x7B.name\x7D\x7D'}
  ></textarea></label>
  <div class="mt-0.5 text-[10px] text-gray-400">Supports Go templates. Data passes through unchanged.</div>
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Upstream data passed through and logged. Also used as context for the message template.">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— Data to log (passed through unchanged)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">any</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="All input data passed through unchanged. This is a pure pass-through node.">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— Same data, unchanged (pass-through)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ ...inputs }"}</div>
      </div>
    </div>
  </div>
</div>
