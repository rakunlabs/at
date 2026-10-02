<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';
  import { modelLabel } from '@/lib/workflow/node-appearance';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
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
    { label: 'Preset', value: data.agent_id ? 'Agent preset' : '' },
    { label: 'Model', value: modelLabel(data.provider, data.model), mono: true },
    { label: 'Max steps', value: data.max_iterations ?? '' },
  ]}
  setup={!data.provider && !data.model && !data.agent_id ? 'Choose a provider and model' : ''}
>
  {#snippet extra()}
    <div class="grid grid-cols-4 border-t border-gray-100 text-center text-[11px] leading-6 text-gray-500 dark:border-dark-border dark:text-dark-text-muted">
      {#each resources as resource (resource)}
        <div class="relative">
          {resource}
          <Handle id={resource} type="input" port="config" accept={['config']} position="bottom" label={resource} />
        </div>
      {/each}
    </div>
  {/snippet}
</WorkflowNode>
