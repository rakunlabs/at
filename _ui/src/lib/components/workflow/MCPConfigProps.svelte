<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">MCP Server URLs</span>
  <textarea
    value={data.mcp_urls?.join('\n') || ''}
    oninput={(e) => { data.mcp_urls = (e.target as HTMLTextAreaElement).value.split('\n').map((s: string) => s.trim()).filter(Boolean); }}
    rows={3}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder="https://mcp-server.example.com/sse"
  ></textarea></label>
  <div class="mt-0.5 text-[10px] text-gray-400">One URL per line</div>
</div>
<div class="mt-1 px-2 py-1.5 bg-orange-50 border border-orange-200 rounded text-[10px] text-orange-700">
  Connect this node's <span class="font-mono font-medium">mcp_urls</span> output to an Agent Call's <span class="font-mono font-medium">mcp</span> input.
</div>
<!-- Port descriptions -->
<div class="border-t border-gray-200 pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="This is a static resource node with no runtime inputs.">
        <span class="text-[11px] text-gray-400 italic">None — static configuration node</span>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Emits the list of MCP server URLs. Connect to an agent_call node's 'mcp' input port.">
        <span class="text-[11px] font-mono font-medium text-gray-700">mcp_urls</span>
        <span class="text-[10px] text-gray-400 ml-1">— List of MCP server URLs for agent_call</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">{"{ mcp_urls: string[] }"}</div>
      </div>
    </div>
  </div>
</div>
