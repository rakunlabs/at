<script lang="ts">
  let { data, providers = [] }: { data: Record<string, any>; providers?: any[] } = $props();

  let selectedProvider = $derived(providers.find(p => p.key === data.provider));
  let availableModels = $derived(
    selectedProvider?.config?.models?.length
      ? selectedProvider.config.models
      : selectedProvider?.config?.model
        ? [selectedProvider.config.model]
        : []
  );
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Provider</span>
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
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Model</span>
  <select
    bind:value={data.model}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
  >
    <option value="">Select model</option>
    {#each availableModels as m}
      <option value={m}>{m}</option>
    {/each}
  </select></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">System Prompt</span>
  <textarea
    bind:value={data.system_prompt}
    rows={3}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder="System prompt (optional)"
  ></textarea></label>
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="The main user message or instruction sent to the LLM. This is required. Falls back to 'text' or 'data' inputs if the prompt port is not connected.">
        <span class="text-[11px] font-mono font-medium text-gray-700">prompt</span>
        <span class="text-[10px] text-gray-400 ml-1">— Main instruction sent to the LLM (required)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">string</div>
      </div>
      <div title="Optional supplementary data appended to the prompt under a 'Context:' header. Use this for reference documents, previous node outputs, or fetched content.">
        <span class="text-[11px] font-mono font-medium text-gray-700">context</span>
        <span class="text-[10px] text-gray-400 ml-1">— Extra reference data appended to prompt (optional)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">string</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Returns a map with the LLM response text. Uses the 'data' port type so it can connect to any downstream node.">
        <span class="text-[11px] font-mono font-medium text-gray-700">response</span>
        <span class="text-[10px] text-gray-400 ml-1">— Map with LLM response, connectable to any node</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ response: string }"}</div>
      </div>
    </div>
  </div>
</div>
