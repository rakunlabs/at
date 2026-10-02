<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';
  import { modelLabel } from '@/lib/workflow/node-appearance';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
</script>

<WorkflowNode {id} type="audio_generate" {data} {selected}
  inputs={[{ id: 'text', port: 'text', accept: ['text', 'data'] }]}
  outputs={[{ id: 'audio', port: 'audio' }, { id: 'metadata', optional: true }]}
  fields={[
    { label: 'Model', value: modelLabel(data.provider, data.model), mono: true },
    { label: 'Voice', value: data.voice },
    { label: 'Format', value: [data.response_format, data.speed != null && data.speed !== 1 ? `${data.speed}×` : ''].filter(Boolean).join(' · ') },
  ]}
  setup={!data.provider ? 'Choose a provider' : ''}
/>
