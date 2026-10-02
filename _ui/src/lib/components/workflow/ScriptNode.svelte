<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  let inputCount = $derived(Math.max(1, Math.min(data.input_count || 1, 10)));
  let inputs = $derived(inputCount === 1 ? [{ id: 'data' }] : Array.from({ length: inputCount }, (_, i) => ({ id: `data${i + 1}` })));
</script>

<WorkflowNode {id} type="script" {data} {selected} {inputs}
  outputs={[{ id: 'result' }, { id: 'true', label: 'returned' }, { id: 'false', label: 'threw' }, { id: 'always', optional: true }]}
  code={data.code}
  setup={!data.code ? 'Write JavaScript' : ''}
/>
