<script lang="ts">
  import { ChevronUp, ChevronDown, Plus, X } from 'lucide-svelte';
  import { newDataCondition, newSwitchRule } from '@/lib/workflow/data-operations';
  import DataConditionEditor from './DataConditionEditor.svelte';
  import DataLiteralEditor from './DataLiteralEditor.svelte';
  let { data, nodeType }: { data: Record<string, any>; nodeType: string } = $props();
  const id = $props.id();
  function moveRule(index: number, direction: number) {
    const rules = [...data.rules];
    [rules[index], rules[index + direction]] = [rules[index + direction], rules[index]];
    data.rules = rules;
  }
</script>

<div class="space-y-4 text-sm text-gray-900 dark:text-dark-text">
  {#if nodeType === 'edit_fields'}
    <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Edit an object, or every object in an array. Names set top-level fields; source paths read the original item. A blank path selects the whole item.</p>
    <label class="flex items-center gap-2 text-xs"><input type="checkbox" checked={data.keep_input !== false} onchange={event => data.keep_input = event.currentTarget.checked} />Keep other input fields</label>
    {#each data.fields || [] as field, i}
      <section class="space-y-2 border-t border-gray-200 pt-3 dark:border-dark-border">
        <div class="flex items-center justify-between"><span class="text-xs font-semibold">Field {i + 1}</span><button onclick={() => data.fields = data.fields.filter((_: unknown, index: number) => index !== i)} aria-label={`Remove field ${i + 1}`} class="p-2"><X size={14} /></button></div>
        <div><label for={`${id}-name-${i}`} class="block text-xs">Output field name</label><input id={`${id}-name-${i}`} bind:value={field.name} placeholder="customer_name" class="mt-1 w-full border border-gray-300 bg-white p-2 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated" /></div>
        <div><label for={`${id}-source-${i}`} class="block text-xs">Source</label><select id={`${id}-source-${i}`} value={field.source || 'value'} onchange={event => field.source = event.currentTarget.value} class="mt-1 w-full border border-gray-300 bg-white p-2 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated"><option value="value">Fixed value</option><option value="path">Input field</option></select></div>
        {#if field.source === 'path'}
          <div><label for={`${id}-path-${i}`} class="block text-xs">Source path (JSON Pointer)</label><input id={`${id}-path-${i}`} bind:value={field.path} placeholder="/customer/name" class="mt-1 w-full border border-gray-300 bg-white p-2 font-mono text-xs dark:border-dark-border-subtle dark:bg-dark-elevated" /></div>
        {:else}<DataLiteralEditor config={field} />{/if}
      </section>
    {/each}
    <button onclick={() => data.fields = [...(data.fields || []), { name: '', source: 'value', path: '', value_type: 'string', value: '' }]} disabled={(data.fields?.length || 0) >= 64} class="flex items-center gap-1 border border-gray-300 px-3 py-2 text-xs disabled:opacity-50 dark:border-dark-border-subtle"><Plus size={14} />Add field</button>
  {:else if nodeType === 'filter'}
    <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Keeps matching items and always returns an array. Nothing matched means an empty array, so a following Aggregate can still count zero.</p>
    <div><label for={`${id}-items`} class="block text-xs">Items path (blank = data)</label><input id={`${id}-items`} bind:value={data.items_path} placeholder="/items" class="mt-1 w-full border border-gray-300 bg-white p-2 font-mono text-xs dark:border-dark-border-subtle dark:bg-dark-elevated" /></div>
    <div><label for={`${id}-match`} class="block text-xs">Match conditions</label><select id={`${id}-match`} bind:value={data.match} class="mt-1 w-full border border-gray-300 bg-white p-2 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated"><option value="all">All conditions (AND)</option><option value="any">Any condition (OR)</option></select></div>
    {#each data.conditions || [] as condition, i}
      <section class="space-y-2 border-t border-gray-200 pt-3 dark:border-dark-border">
        <div class="flex items-center justify-between"><span class="text-xs font-semibold">Condition {i + 1}</span><button onclick={() => data.conditions = data.conditions.filter((_: unknown, index: number) => index !== i)} aria-label={`Remove condition ${i + 1}`} class="p-2"><X size={14} /></button></div>
        <DataConditionEditor {condition} />
      </section>
    {/each}
    <button onclick={() => data.conditions = [...(data.conditions || []), newDataCondition()]} disabled={(data.conditions?.length || 0) >= 32} class="flex items-center gap-1 border border-gray-300 px-3 py-2 text-xs disabled:opacity-50 dark:border-dark-border-subtle"><Plus size={14} />Add condition</button>
  {:else if nodeType === 'switch'}
    <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Routes the whole payload. Paths refer to data; use Loop first for per-item routing. No matches use Fallback. Reordering or renaming cases keeps their connections.</p>
    <div><label for={`${id}-mode`} class="block text-xs">Matching rules</label><select id={`${id}-mode`} bind:value={data.match_mode} class="mt-1 w-full border border-gray-300 bg-white p-2 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated"><option value="first">First matching case</option><option value="all">All matching cases</option></select></div>
    {#each data.rules || [] as rule, i}
      <section class="space-y-2 border-t border-gray-200 pt-3 dark:border-dark-border">
        <div class="flex items-center justify-between"><span class="text-xs font-semibold">Case {i + 1}</span><div class="flex"><button onclick={() => moveRule(i, -1)} disabled={i === 0} aria-label={`Move case ${i + 1} up`} class="p-2 disabled:opacity-40"><ChevronUp size={14} /></button><button onclick={() => moveRule(i, 1)} disabled={i === data.rules.length - 1} aria-label={`Move case ${i + 1} down`} class="p-2 disabled:opacity-40"><ChevronDown size={14} /></button><button onclick={() => data.rules = data.rules.filter((_: unknown, index: number) => index !== i)} aria-label={`Remove case ${i + 1}`} class="p-2"><X size={14} /></button></div></div>
        <div><label for={`${id}-label-${i}`} class="block text-xs">Case label</label><input id={`${id}-label-${i}`} bind:value={rule.label} class="mt-1 w-full border border-gray-300 bg-white p-2 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated" /></div>
        <DataConditionEditor condition={rule} />
      </section>
    {/each}
    <button onclick={() => data.rules = [...(data.rules || []), newSwitchRule(data.rules || [])]} disabled={(data.rules?.length || 0) >= 16} class="flex items-center gap-1 border border-gray-300 px-3 py-2 text-xs disabled:opacity-50 dark:border-dark-border-subtle"><Plus size={14} />Add case</button>
  {:else if nodeType === 'merge'}
    <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Connect one source to each input. An inactive branch is an empty side; both inactive means this node is skipped. Operates within one invocation, not across independent Loops.</p>
    <div><label for={`${id}-mode`} class="block text-xs">Combine mode</label><select id={`${id}-mode`} bind:value={data.mode} class="mt-1 w-full border border-gray-300 bg-white p-2 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated"><option value="append">Append — left items, then right items</option><option value="zip">Pair by position — keep unmatched items</option><option value="join">Match by key</option></select></div>
    {#if data.mode === 'join'}
      <div><label for={`${id}-join`} class="block text-xs">Keep rows</label><select id={`${id}-join`} bind:value={data.join_type} class="mt-1 w-full border border-gray-300 bg-white p-2 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated"><option value="inner">Matches only (inner join)</option><option value="left">All left items (left join)</option><option value="outer">All items (full outer join)</option></select></div>
      <div><label for={`${id}-left`} class="block text-xs">Left key path</label><input id={`${id}-left`} bind:value={data.left_key} placeholder="/id" class="mt-1 w-full border border-gray-300 bg-white p-2 font-mono text-xs dark:border-dark-border-subtle dark:bg-dark-elevated" /></div>
      <div><label for={`${id}-right`} class="block text-xs">Right key path</label><input id={`${id}-right`} bind:value={data.right_key} placeholder="/customer_id" class="mt-1 w-full border border-gray-300 bg-white p-2 font-mono text-xs dark:border-dark-border-subtle dark:bg-dark-elevated" /></div>
      <p class="text-xs text-gray-600 dark:text-dark-text-secondary">Keys are non-null scalar values; numbers and text do not coerce. Missing/null keys do not match. Duplicate keys produce every matching pair, up to 10,000 output items.</p>
    {/if}
    {#if data.mode !== 'append'}<p class="text-xs text-gray-600 dark:text-dark-text-secondary">Each output item has left and right fields. Unmatched sides are null; same-named fields never overwrite each other.</p>{/if}
  {:else if nodeType === 'aggregate'}
    <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Summarizes an array in this invocation, not all Loop invocations. Output is an object with value and count fields.</p>
    <div><label for={`${id}-operation`} class="block text-xs">Operation</label><select id={`${id}-operation`} bind:value={data.operation} class="mt-1 w-full border border-gray-300 bg-white p-2 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated"><option value="collect">Collect values</option><option value="count">Count items</option><option value="sum">Sum</option><option value="average">Average</option><option value="min">Minimum</option><option value="max">Maximum</option></select></div>
    <div><label for={`${id}-items`} class="block text-xs">Array path (blank = data)</label><input id={`${id}-items`} bind:value={data.items_path} placeholder="/items" class="mt-1 w-full border border-gray-300 bg-white p-2 font-mono text-xs dark:border-dark-border-subtle dark:bg-dark-elevated" /></div>
    {#if data.operation !== 'count'}<div><label for={`${id}-field`} class="block text-xs">Value path in each item (blank = whole item)</label><input id={`${id}-field`} bind:value={data.field_path} placeholder="/amount" class="mt-1 w-full border border-gray-300 bg-white p-2 font-mono text-xs dark:border-dark-border-subtle dark:bg-dark-elevated" /></div>{/if}
    <p class="text-xs text-gray-600 dark:text-dark-text-secondary">An empty array gives count/sum 0, collected values [], and average/min/max null. Numeric operations reject missing or nonnumeric values.</p>
  {/if}
  {#if nodeType === 'filter' || nodeType === 'switch'}<p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">Comparisons preserve JSON types. Missing paths do not match, except Does not exist. Null, false and zero are distinct. A blank path selects the whole item/payload.</p>{/if}
</div>
