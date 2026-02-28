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
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Max Iterations</span>
  <input
    type="number"
    bind:value={data.max_iterations}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
    placeholder="10"
    min="0"
  /></label>
  <div class="mt-0.5 text-[10px] text-gray-400">0 = unlimited</div>
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="The main user message or instruction sent to the agent. Required. Falls back to 'text' or 'data' inputs if the prompt port is not connected.">
        <span class="text-[11px] font-mono font-medium text-gray-700">prompt</span>
        <span class="text-[10px] text-gray-400 ml-1">— Main instruction sent to the agent (required)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">string</div>
      </div>
      <div title="Optional supplementary data appended to the prompt under a 'Context:' header.">
        <span class="text-[11px] font-mono font-medium text-gray-700">context</span>
        <span class="text-[10px] text-gray-400 ml-1">— Extra reference data appended to prompt (optional)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">string</div>
      </div>
      <div title="Additional MCP server URLs merged with static config. Connect from an mcp_config node.">
        <span class="text-[11px] font-mono font-medium text-gray-700">mcp</span>
        <span class="text-[10px] text-gray-400 ml-1">— MCP server URLs from mcp_config node (optional)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">string[]</div>
      </div>
      <div title="Additional skill names merged with static config. Connect from a skill_config node.">
        <span class="text-[11px] font-mono font-medium text-gray-700">skills</span>
        <span class="text-[10px] text-gray-400 ml-1">— Skill names from skill_config node (optional)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">string[]</div>
      </div>
      <div title="Memory/context data appended to the prompt as 'Memory:' block. Connect from a memory_config node.">
        <span class="text-[11px] font-mono font-medium text-gray-700">memory</span>
        <span class="text-[10px] text-gray-400 ml-1">— Memory data from memory_config node (optional)</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">any</div>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="The final LLM response text after the agentic loop completes (all tool calls resolved).">
        <span class="text-[11px] font-mono font-medium text-gray-700">response</span>
        <span class="text-[10px] text-gray-400 ml-1">— Final agent response after tool-call loop</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ response: string }"}</div>
      </div>
    </div>
  </div>
</div>
