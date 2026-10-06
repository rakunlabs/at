<script lang="ts" module>
  import { listAgents, type Agent } from '@/lib/api/agents';

  let cached: Promise<Agent[]> | null = null;
  function agentNames(): Promise<Agent[]> {
    cached ??= listAgents({ _limit: 500 }).then(res => res.data ?? []).catch(() => {
      cached = null;
      return [];
    });
    return cached;
  }
</script>

<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';
  import { modelLabel } from '@/lib/workflow/node-appearance';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();

  // Shared across every Agent Call node on the canvas.
  let agents = $state<Agent[]>([]);
  agentNames().then(list => agents = list);
  let agentName = $derived(agents.find(a => a.id === data.agent_id)?.name || (data.agent_id ? 'Agent' : ''));
  const resources = ['skills', 'mcp', 'memory', 'agents'];
</script>

<WorkflowNode {id} type="agent_call" {data} {selected}
  inputs={[
    { id: 'prompt', port: 'text', accept: ['text', 'data'] },
    { id: 'context' },
    { id: 'attachments', optional: true },
  ]}
  outputs={[
    { id: 'response', port: 'text' },
    { id: 'files', optional: true },
    { id: 'image', optional: true },
  ]}
  fields={[
    { label: 'Agent', value: agentName },
    { label: 'Model', value: modelLabel(data.provider, data.model), mono: true },
    { label: 'Max steps', value: data.max_iterations ?? '' },
  ]}
  setup={!data.provider && !data.model && !data.agent_id ? 'Choose an agent' : ''}
>
  {#snippet extra()}
    <div class="grid grid-cols-4 border-t text-center text-[11px] leading-6 border-dark-border text-dark-text-muted">
      {#each resources as resource (resource)}
        <div class="relative">
          {resource}
          <Handle id={resource} type="input" port="config" accept={['config']} position="bottom" label={resource} />
        </div>
      {/each}
    </div>
  {/snippet}
</WorkflowNode>
