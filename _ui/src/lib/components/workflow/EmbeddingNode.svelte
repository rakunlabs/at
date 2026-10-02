<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';
  import { modelLabel } from '@/lib/workflow/node-appearance';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
</script>

<WorkflowNode {id} type="embedding" {data} {selected}
  inputs={[{ id: 'input', port: 'text', accept: ['text', 'data'] }]}
  outputs={[{ id: 'embedding', port: 'embedding' }, { id: 'data', optional: true }]}
  fields={[
    { label: 'Model', value: modelLabel(data.provider, data.model), mono: true },
    { label: 'Dimensions', value: data.dimensions || '' },
  ]}
  setup={!data.provider && !data.model ? 'Choose a provider and model' : ''}
/>
