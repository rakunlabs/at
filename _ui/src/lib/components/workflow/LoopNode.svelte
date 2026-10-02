<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
</script>

<WorkflowNode {id} type="loop" {data} {selected}
  inputs={[{ id: 'input', label: 'data' }]}
  outputs={[{ id: 'item' }]}
  code={data.expression}
  setup={!data.expression ? 'Choose the items to iterate' : ''}
/>
