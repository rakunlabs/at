<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';
  import { modelLabel } from '@/lib/workflow/node-appearance';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();

  let questionIds = $derived.by(() => {
    let q = data.questions;
    if (typeof q === 'string') {
      try { q = JSON.parse(q); } catch { return []; }
    }
    return q && typeof q === 'object' ? Object.keys(q) : [];
  });
</script>

<WorkflowNode {id} type="decision" {data} {selected}
  inputs={[{ id: 'state' }]}
  outputs={[{ id: 'decided' }, { id: 'escalate' }]}
  fields={[
    { label: 'Model', value: data.provider ? modelLabel(data.provider, data.model) : '', mono: true },
    { label: 'Escalate below', value: data.min_confidence || '' },
  ]}
  tags={questionIds}
  setup={!data.provider ? 'Choose a decision provider' : ''}
/>
