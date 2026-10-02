<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
</script>

<WorkflowNode {id} type="workflow_call" {data} {selected}
  inputs={[{ id: 'inputs', accept: undefined }]}
  outputs={[{ id: 'output' }]}
  fields={[{ label: 'Calls', value: data.workflow_name || data.workflow_id }]}
  setup={!data.workflow_id ? 'Select a workflow' : ''}
/>
