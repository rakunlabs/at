<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div class="mt-1 px-2 py-1.5 bg-teal-50 border border-teal-200 rounded text-[10px] text-teal-700">
  Connect upstream data to this node's <span class="font-mono font-medium">data</span> input, then connect the <span class="font-mono font-medium">memory</span> output to an Agent Call's <span class="font-mono font-medium">memory</span> input.
</div>
<div class="text-[10px] text-gray-400 mt-1">Passes upstream data as additional context to the agent.</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Arbitrary upstream data to forward as memory context. If no 'data' key exists, all inputs are merged.">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— Upstream data to forward as memory</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">any</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="The memory data passed through. Connect to an agent_call node's 'memory' input port.">
        <span class="text-[11px] font-mono font-medium text-gray-700">memory</span>
        <span class="text-[10px] text-gray-400 ml-1">— Memory data for agent_call</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ memory: any }"}</div>
      </div>
    </div>
  </div>
</div>
