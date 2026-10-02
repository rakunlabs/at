<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let fields = $derived<string[]>(Array.isArray(data.fields) ? data.fields : []);
</script>

<WorkflowNode {id} type="output" {data} {selected}
  inputs={[{ id: 'input', label: 'in' }]}
  tags={fields}
  empty="Returns its input as the workflow result"
/>
