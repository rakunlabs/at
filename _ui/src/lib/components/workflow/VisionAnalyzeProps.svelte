<script lang="ts">
  let { data = $bindable(), providers = [], workflow }: { data: Record<string, any>; providers?: any[]; workflow?: any } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Provider</span>
  <select
    bind:value={data.provider}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
  >
    <option value="">Select provider</option>
    {#each providers as p}
      <option value={p.key}>{p.key}</option>
    {/each}
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Model</span>
  <input
    type="text"
    bind:value={data.model}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="gpt-4o"
  /></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">System Prompt</span>
  <textarea
    bind:value={data.system_prompt}
    rows={4}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder="Describe what to analyze in the image..."
  ></textarea></label>
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Image URL or base64-encoded image data to analyze.">
        <span class="text-[11px] font-mono font-medium text-gray-700">image</span>
        <span class="text-[10px] text-gray-400 ml-1">— Image URL or base64 data (required)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">string</div>
      </div>
      <div title="Optional text prompt describing what to look for.">
        <span class="text-[11px] font-mono font-medium text-gray-700">prompt</span>
        <span class="text-[10px] text-gray-400 ml-1">— Analysis instruction (optional)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">string</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Analysis result from the vision model.">
        <span class="text-[11px] font-mono font-medium text-gray-700">response</span>
        <span class="text-[10px] text-gray-400 ml-1">— Vision model analysis result</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ response: string }"}</div>
      </div>
    </div>
  </div>
</div>
