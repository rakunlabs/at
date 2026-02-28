<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="This is a source/entry node with no inputs.">
        <span class="text-[11px] text-gray-400 italic">None — workflow entry point</span>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="The original workflow trigger inputs (run-time data passed when the workflow is invoked).">
        <span class="text-[11px] font-mono font-medium text-gray-700">data</span>
        <span class="text-[10px] text-gray-400 ml-1">— Workflow trigger inputs</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ data: map }"}</div>
      </div>
    </div>
  </div>
</div>
