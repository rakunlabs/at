<script lang="ts">
  import { getFlow, type NodeProps } from 'kaykay';
  import { switchOutputPorts } from '@/lib/workflow/data-operations';
  import { canvasInputHandle } from '@/lib/workflow/ports';
  import WorkflowNode from './WorkflowNode.svelte';

  let { id, data, selected }: NodeProps<Record<string, any>> = $props();
  const flow = getFlow();
  let type = $derived(flow.getNode(id)?.type || 'edit_fields');
  let inputs = $derived(type === 'merge' ? [{ id: 'left' }, { id: 'right' }] : [{ id: canvasInputHandle(type, 'data'), label: 'data' }]);
  let outputs = $derived(type === 'switch' ? switchOutputPorts(data) : [{ id: 'data' }]);

  function count(value: unknown, one: string, many: string): string {
    const n = Array.isArray(value) ? value.length : 0;
    return `${n} ${n === 1 ? one : many}`;
  }

  let summary = $derived(
    type === 'edit_fields' ? `${count(data.fields, 'field', 'fields')} · ${data.keep_input !== false ? 'keeps other fields' : 'selected fields only'}`
    : type === 'filter' ? `${count(data.conditions, 'condition', 'conditions')} · ${data.match || 'all'} must match`
    : type === 'switch' ? `${count(data.rules, 'case', 'cases')} · ${data.match_mode || 'first'} match`
    : type === 'merge' ? `${data.mode || 'append'}${data.mode === 'join' ? ` · ${data.join_type || 'inner'} join` : ''}`
    : `${data.operation || 'collect'} · ${data.field_path || 'whole items'}`,
  );
</script>

<WorkflowNode {id} {type} {data} {selected} {inputs} {outputs} empty={summary} />
