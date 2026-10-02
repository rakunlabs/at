<script lang="ts">
  import { Handle, type NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
</script>

<WorkflowNode {id} type="agent_config" {data} {selected}
  fields={[{ label: 'Agent', value: data.agent_id, mono: true }]}
  setup={!data.agent_id ? 'Select an agent' : ''}
>
  {#snippet extra()}<Handle id="agent" type="output" port="config" position="top" label="agent" />{/snippet}
</WorkflowNode>
