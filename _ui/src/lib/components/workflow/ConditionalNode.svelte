<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
</script>

<WorkflowNode {id} type="conditional" {data} {selected}
  inputs={[{ id: 'input', label: 'data' }]}
  outputs={[{ id: 'true' }, { id: 'false' }]}
  code={data.expression}
  setup={!data.expression ? 'Write a condition' : ''}
/>
