<script lang="ts">
  import DataLiteralEditor from './DataLiteralEditor.svelte';
  let { condition }: { condition: Record<string, any> } = $props();
  const id = $props.id();
  const operators = [ ['eq', 'Equals'], ['neq', 'Does not equal'], ['gt', 'Greater than'], ['gte', 'Greater than or equal'], ['lt', 'Less than'], ['lte', 'Less than or equal'], ['contains', 'Contains'], ['exists', 'Exists (including null)'], ['not_exists', 'Does not exist'], ['is_empty', 'Is empty / null'] ];
</script>

<div class="space-y-2">
  <div><label for={`${id}-path`} class="block text-xs text-dark-text-secondary">Field path (JSON Pointer)</label>
    <input id={`${id}-path`} bind:value={condition.path} placeholder="/status · blank = whole item" class="mt-1 w-full border p-2 font-mono text-xs border-dark-border-subtle bg-dark-elevated text-dark-text" />
  </div>
  <div><label for={`${id}-operator`} class="block text-xs text-dark-text-secondary">Condition</label>
    <select id={`${id}-operator`} bind:value={condition.operator} class="mt-1 w-full border p-2 text-xs border-dark-border-subtle bg-dark-elevated text-dark-text">
      {#each operators as [value, label]}<option {value}>{label}</option>{/each}
    </select>
  </div>
  {#if !['exists', 'not_exists', 'is_empty'].includes(condition.operator)}<DataLiteralEditor config={condition} />{/if}
</div>
