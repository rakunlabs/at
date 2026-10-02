<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let payloadKeys = $derived(data.payload && typeof data.payload === 'object' ? Object.keys(data.payload).length : 0);
</script>

<WorkflowNode {id} type="cron_trigger" {data} {selected}
  outputs={[{ id: 'output', label: 'out' }]}
  fields={[
    { label: 'Schedule', value: data.schedule, mono: true },
    { label: 'Timezone', value: data.timezone },
    { label: 'Payload', value: payloadKeys ? `${payloadKeys} ${payloadKeys === 1 ? 'field' : 'fields'}` : '' },
  ]}
  setup={!data.schedule ? 'Set a cron schedule' : ''}
/>
