<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let fields = $derived<{ name: string; type?: string }[]>(Array.isArray(data.fields) ? data.fields : []);
</script>

<WorkflowNode {id} type="input" {data} {selected}
  outputs={[{ id: 'output', label: 'out' }]}
  fields={fields.map(field => ({ label: field.name, value: field.type || 'string', mono: true }))}
  empty="Receives the data the workflow starts with"
/>
