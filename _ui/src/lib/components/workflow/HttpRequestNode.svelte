<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let flags = $derived([
    data.proxy && 'proxy', data.insecure_skip_verify && 'insecure TLS', data.retry && 'retry', data.save_response && 'save to file',
  ].filter(Boolean) as string[]);
</script>

<WorkflowNode {id} type="http_request" {data} {selected}
  inputs={[{ id: 'values', accept: ['data'] }, { id: 'data' }]}
  outputs={[{ id: 'success' }, { id: 'error' }, { id: 'always', optional: true }]}
  fields={[
    { label: data.method || 'GET', value: data.url, mono: true },
    { label: 'Timeout', value: data.timeout && data.timeout !== 30 ? `${data.timeout}s` : '' },
  ]}
  tags={flags}
  setup={!data.url ? 'Enter a URL' : ''}
/>
