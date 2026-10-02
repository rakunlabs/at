<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let tags = $derived([
    data.content_type === 'text/html' && 'HTML', data.attachments && 'attachments', data.inline_images && 'inline images',
  ].filter(Boolean) as string[]);
</script>

<WorkflowNode {id} type="email" {data} {selected}
  inputs={[
    { id: 'values', accept: ['data'] },
    { id: 'data' },
    { id: 'attachments', optional: true },
    { id: 'inline_images', label: 'inline images', optional: true },
  ]}
  outputs={[{ id: 'success' }, { id: 'error' }, { id: 'always', optional: true }]}
  fields={[
    { label: 'To', value: data.to, mono: true },
    { label: 'Subject', value: data.subject },
  ]}
  {tags}
  setup={!data.to && !data.subject ? 'Set recipients and subject' : ''}
/>
