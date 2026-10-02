<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let approval = $derived(data.mode === 'approval');
</script>

<WorkflowNode {id} type="wait" {data} {selected}
  inputs={[{ id: 'data_in', label: 'data' }]}
  outputs={[{ id: 'data' }]}
  fields={[{ label: approval ? 'Approval' : 'Wait', value: approval ? 'Required' : `${data.seconds ?? 60} seconds` }]}
  code={approval ? data.prompt : ''}
/>
