<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<!-- Description -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Description</span>
    <p class="mt-1 text-[11px] text-gray-500 leading-relaxed">
      Workflow entry point. Passes run-time inputs downstream as-is. The output shape depends on how the workflow is triggered.
    </p>
  </div>
</div>

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
        <span class="text-[10px] text-gray-400 ml-1">— Workflow trigger inputs (pass-through)</span>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Shape</span>
    <div class="mt-1 space-y-1.5 text-[10px] text-gray-500">
      <div>
        <span class="font-medium text-gray-600">Text mode</span>
        <div class="font-mono text-gray-400 ml-2 mt-0.5">{"{ text: \"user input\" }"}</div>
      </div>
      <div>
        <span class="font-medium text-gray-600">JSON mode</span>
        <div class="font-mono text-gray-400 ml-2 mt-0.5">{"{ key: value, ... }"}</div>
      </div>
      <div>
        <span class="font-medium text-gray-600">API / webhook</span>
        <div class="font-mono text-gray-400 ml-2 mt-0.5">Whatever is passed in the request body's <span class="font-medium">inputs</span> field</div>
      </div>
    </div>
  </div>
</div>
