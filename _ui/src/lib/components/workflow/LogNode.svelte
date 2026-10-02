<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
</script>

<WorkflowNode {id} type="log" {data} {selected}
  inputs={[{ id: 'data' }]}
  outputs={[{ id: 'output', label: 'data' }]}
  fields={[{ label: 'Level', value: data.level || 'info' }]}
  code={data.message}
/>
