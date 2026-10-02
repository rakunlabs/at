<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
</script>

<WorkflowNode {id} type="http_trigger" {data} {selected}
  outputs={[{ id: 'output', label: 'out' }]}
  fields={[
    { label: 'Path', value: data.trigger_id ? `/webhooks/${data.alias || data.trigger_id}` : 'Generated when saved', mono: !!data.trigger_id },
    { label: 'Access', value: data.public ? 'Public' : 'Token required' },
  ]}
/>
