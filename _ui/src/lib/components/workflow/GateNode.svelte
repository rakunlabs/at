<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
</script>

<WorkflowNode {id} type="gate" {data} {selected}
  inputs={[{ id: 'data_in', label: 'data' }, { id: 'signal' }]}
  outputs={[{ id: 'data' }]}
  empty={`Continues once the signal arrives${data.pass_signal ? ' · passes the signal on' : ''}`}
/>
