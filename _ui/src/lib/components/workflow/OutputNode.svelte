<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import { outputFieldNames } from '@/lib/workflow/output-fields';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let fields = $derived(outputFieldNames(data.fields));
  let inputs = $derived([{ id: 'input', label: 'in' }, ...fields.map(name => ({ id: name, label: name }))]);
  let mode = $derived(data.response_mode === 'file' ? 'Responds with a file' : data.response_mode === 'multipart' ? 'Responds with JSON + files' : '');
</script>

<WorkflowNode {id} type="output" {data} {selected}
  {inputs}
  fields={[{ label: 'HTTP', value: mode }]}
  empty="Returns its input as the workflow result"
/>
