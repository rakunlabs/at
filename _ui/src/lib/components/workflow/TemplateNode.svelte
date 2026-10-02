<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let variables = $derived<string[]>(Array.isArray(data.variables) ? data.variables : []);
</script>

<WorkflowNode {id} type="template" {data} {selected}
  inputs={[{ id: 'input', label: 'data' }]}
  outputs={[{ id: 'output', label: 'text', port: 'text' }]}
  code={data.template}
  tags={variables.map(v => `{{.${v}}}`)}
  setup={!data.template ? 'Write the template' : ''}
/>
