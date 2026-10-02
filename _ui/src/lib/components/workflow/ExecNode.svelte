<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let inputCount = $derived(Math.max(1, Math.min(data.input_count || 1, 10)));
  let inputs = $derived(inputCount === 1 ? [{ id: 'data' }] : Array.from({ length: inputCount }, (_, i) => ({ id: `data${i + 1}` })));
</script>

<WorkflowNode {id} type="exec" {data} {selected} {inputs}
  outputs={[{ id: 'true', label: 'ok' }, { id: 'false', label: 'fail' }, { id: 'always', optional: true }]}
  fields={[
    { label: 'Directory', value: data.working_dir, mono: true },
    { label: 'Timeout', value: data.timeout && data.timeout !== 60 ? `${data.timeout}s` : '' },
  ]}
  code={data.command}
  setup={!data.command ? 'Write a shell command' : ''}
/>
