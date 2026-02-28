<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="All incoming data from upstream nodes, merged into a single map. The first output node to fire sends its result for synchronous API responses.">
        <span class="text-[11px] font-mono font-medium text-gray-700">input</span>
        <span class="text-[10px] text-gray-400 ml-1">— All upstream data merged (workflow result)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">map — all upstream keys pass through to workflow result</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="This is a terminal/sink node with no outputs.">
        <span class="text-[11px] text-gray-400 italic">None — terminal sink node</span>
      </div>
    </div>
  </div>
</div>
