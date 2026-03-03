<script lang="ts">
  import { listAgents, type Agent } from '@/lib/api/agents';

  let { data }: { data: Record<string, any> } = $props();

  let agents = $state<Agent[]>([]);

  listAgents().then(res => agents = res.data).catch(() => {});
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Select Agent</span>
  <select
    bind:value={data.agent_id}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400"
  >
    <option value="">Select an agent</option>
    {#each agents as a}
      <option value={a.id}>{a.name}</option>
    {/each}
  </select></label>
  <div class="mt-0.5 text-[10px] text-gray-400">Select an agent to be used as a delegate tool.</div>
</div>

<div class="mt-2 px-2 py-1.5 bg-blue-50 border border-blue-200 rounded text-[10px] text-blue-700">
  Connect this node's <span class="font-mono font-medium">agent</span> output to an Agent Call's <span class="font-mono font-medium">agents</span> input.
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
      <div title="Emits the selected agent ID. Connect to an agent_call node's 'agents' input port.">
        <span class="text-[11px] font-mono font-medium text-gray-700">agent</span>
        <span class="text-[10px] text-gray-400 ml-1">— Agent ID for agent_call</span>
        <div class="text-[10px] font-mono text-gray-400 ml-2 mt-0.5">string</div>
      </div>
    </div>
  </div>
</div>
