<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import { outputFieldNames } from '@/lib/workflow/output-fields';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let outputs = $derived([
    { id: 'output' },
    { id: 'files', optional: true },
    ...outputFieldNames(data.output_fields).map(name => ({ id: name, label: name })),
  ]);
</script>

<WorkflowNode {id} type="workflow_call" {data} {selected}
  inputs={[{ id: 'inputs', accept: undefined }]}
  {outputs}
  fields={[{ label: 'Calls', value: data.workflow_name || data.workflow_id }]}
  setup={!data.workflow_id ? 'Select a workflow' : ''}
/>
