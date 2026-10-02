<script lang="ts">
  import type { NodeProps } from 'kaykay';
  import WorkflowNode from './WorkflowNode.svelte';
  import { modelLabel } from '@/lib/workflow/node-appearance';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
</script>

<WorkflowNode {id} type="audio_transcribe" {data} {selected}
  inputs={[{ id: 'audio', port: 'audio', accept: ['audio', 'text', 'data'] }]}
  outputs={[{ id: 'text', port: 'text' }, { id: 'segments', optional: true }]}
  fields={[
    { label: 'Model', value: modelLabel(data.provider, data.model), mono: true },
    { label: 'Language', value: data.language },
    { label: 'Format', value: data.response_format },
  ]}
  setup={!data.provider ? 'Choose a provider' : ''}
/>
