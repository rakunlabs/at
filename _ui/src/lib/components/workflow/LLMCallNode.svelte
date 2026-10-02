<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';
  import { modelLabel } from '@/lib/workflow/node-appearance';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
</script>

<WorkflowNode {id} type="llm_call" {data} {selected}
  inputs={[
    { id: 'prompt', port: 'text', accept: ['text', 'data'] },
    { id: 'context' },
    { id: 'attachments', optional: true },
  ]}
  outputs={[
    { id: 'response', port: 'text' },
    ...(data.output_format === 'json' ? [{ id: 'json' }] : []),
    { id: 'files', optional: true },
    { id: 'image', optional: true },
  ]}
  fields={[
    { label: 'Model', value: modelLabel(data.provider, data.model), mono: true },
    { label: 'Output', value: data.output_format && data.output_format !== 'text' ? data.output_format.toUpperCase() : '' },
    { label: 'System', value: data.system_prompt },
  ]}
  setup={!data.provider && !data.model ? 'Choose a provider and model' : ''}
/>
