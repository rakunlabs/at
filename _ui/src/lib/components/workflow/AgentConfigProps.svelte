<script lang="ts">
  import { listAgents, type Agent } from '@/lib/api/agents';

  let { data }: { data: Record<string, any> } = $props();

  let agents = $state<Agent[]>([]);

  listAgents().then(res => agents = res.data).catch(() => {});
</script>

<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Select Agent</span>
  <select
    bind:value={data.agent_id}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-dark-border-subtle focus:outline-none focus:ring-1 focus:ring-dark-border-subtle"
  >
    <option value="">Select an agent</option>
    {#each agents as a}
      <option value={a.id}>{a.name}</option>
    {/each}
  </select></label>
  <div class="mt-0.5 text-[10px] text-dark-text-muted">Select an agent to be used as a delegate tool.</div>
</div>

<div class="mt-2 px-2 py-1.5 bg-blue-900/20 border border-blue-900/60 text-[10px] text-blue-300">
  Connect this node's <span class="font-mono font-medium">agent</span> output to an Agent Call's <span class="font-mono font-medium">agents</span> input.
</div>

<!-- Port descriptions -->
<div class="border-t border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Input Ports</span>
    <div class="mt-1 space-y-1">
      <div title="This is a static resource node with no runtime inputs.">
        <span class="text-[11px] text-dark-text-muted italic">None — static configuration node</span>
      </div>
    </div>
  </div>
  <div>
    <span class="text-[11px] font-medium text-dark-text-muted">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div title="Emits the selected agent ID. Connect to an agent_call node's 'agents' input port.">
        <span class="text-[11px] font-mono font-medium text-dark-text-secondary">agent</span>
        <span class="text-[10px] text-dark-text-muted ml-1">— Agent ID for agent_call</span>
        <div class="text-[10px] font-mono text-dark-text-muted ml-2 mt-0.5">string</div>
      </div>
    </div>
  </div>
</div>
