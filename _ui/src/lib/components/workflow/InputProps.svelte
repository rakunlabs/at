<script lang="ts">
  import { Plus, Trash2, ChevronDown, ChevronRight } from 'lucide-svelte';

  let { data }: { data: Record<string, any> } = $props();

  interface InputField {
    name: string;
    type: string;
    description: string;
    default: any;
    options: string[];
  }

  // Ensure fields array exists
  if (!Array.isArray(data.fields)) {
    data.fields = [];
  }

  let showFields = $state(data.fields.length > 0);
  let editingIndex = $state<number | null>(null);

  const fieldTypes = ['string', 'number', 'boolean', 'select', 'textarea'];

  function addField() {
    data.fields = [...data.fields, { name: '', type: 'string', description: '', default: '', options: [] }];
    editingIndex = data.fields.length - 1;
    showFields = true;
  }

  function removeField(index: number) {
    data.fields = data.fields.filter((_: any, i: number) => i !== index);
    if (editingIndex === index) editingIndex = null;
  }

  function moveField(index: number, direction: -1 | 1) {
    const newIndex = index + direction;
    if (newIndex < 0 || newIndex >= data.fields.length) return;
    const arr = [...data.fields];
    [arr[index], arr[newIndex]] = [arr[newIndex], arr[index]];
    data.fields = arr;
    if (editingIndex === index) editingIndex = newIndex;
  }
</script>

<!-- Input Fields Builder -->
<div class="border-t border-gray-200 dark:border-dark-border pt-2 mt-2 space-y-2">
  <div class="flex items-center justify-between">
    <button
      onclick={() => { showFields = !showFields; }}
      class="flex items-center gap-1 text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider hover:text-gray-700 dark:hover:text-dark-text-secondary"
    >
      {#if showFields}<ChevronDown size={10} />{:else}<ChevronRight size={10} />{/if}
      Input Fields ({data.fields.length})
    </button>
    <button
      onclick={addField}
      class="flex items-center gap-0.5 text-[10px] text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary"
      title="Add field"
    >
      <Plus size={10} />
      Add
    </button>
  </div>

  {#if showFields}
    {#if data.fields.length === 0}
      <div class="text-[10px] text-gray-400 dark:text-dark-text-muted italic py-2">
        No fields defined. Add fields to create a form for this input.
      </div>
    {:else}
      <div class="space-y-1.5">
        {#each data.fields as field, i}
          <div class="border border-gray-200 dark:border-dark-border rounded overflow-hidden">
            <!-- Field header (click to expand) -->
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <div
              onclick={() => { editingIndex = editingIndex === i ? null : i; }}
              class="w-full flex items-center gap-1.5 px-2 py-1 cursor-pointer hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
            >
              {#if editingIndex === i}<ChevronDown size={9} class="text-gray-400 shrink-0" />{:else}<ChevronRight size={9} class="text-gray-400 shrink-0" />{/if}
              <span class="text-[11px] font-mono font-medium text-gray-700 dark:text-dark-text-secondary truncate">{field.name || '(unnamed)'}</span>
              <span class="text-[9px] text-gray-400 dark:text-dark-text-muted ml-auto shrink-0">{field.type || 'string'}</span>
              <button
                onclick={(e) => { e.stopPropagation(); removeField(i); }}
                class="p-0.5 text-gray-300 hover:text-red-500 dark:text-dark-text-faint dark:hover:text-red-400 shrink-0"
                title="Remove"
              >
                <Trash2 size={10} />
              </button>
            </div>

            <!-- Field editor (expanded) -->
            {#if editingIndex === i}
              <div class="px-2 pb-2 pt-1 space-y-1.5 border-t border-gray-100 dark:border-dark-border bg-gray-50/50 dark:bg-dark-base/30">
                <div>
                  <span class="text-[9px] text-gray-400 dark:text-dark-text-muted">Name</span>
                  <input
                    type="text"
                    bind:value={field.name}
                    placeholder="field_name"
                    class="w-full px-1.5 py-0.5 text-[11px] font-mono border border-gray-200 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
                  />
                </div>
                <div>
                  <span class="text-[9px] text-gray-400 dark:text-dark-text-muted">Type</span>
                  <select
                    bind:value={field.type}
                    class="w-full px-1.5 py-0.5 text-[11px] border border-gray-200 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
                  >
                    {#each fieldTypes as t}
                      <option value={t}>{t}</option>
                    {/each}
                  </select>
                </div>
                <div>
                  <span class="text-[9px] text-gray-400 dark:text-dark-text-muted">Description</span>
                  <input
                    type="text"
                    bind:value={field.description}
                    placeholder="What this field is for"
                    class="w-full px-1.5 py-0.5 text-[11px] border border-gray-200 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
                  />
                </div>
                <div>
                  <span class="text-[9px] text-gray-400 dark:text-dark-text-muted">Default value</span>
                  {#if field.type === 'boolean'}
                    <label class="flex items-center gap-1.5">
                      <input type="checkbox" bind:checked={field.default} class="w-3 h-3" />
                      <span class="text-[11px] text-gray-500">{field.default ? 'true' : 'false'}</span>
                    </label>
                  {:else if field.type === 'number'}
                    <input
                      type="number"
                      bind:value={field.default}
                      class="w-full px-1.5 py-0.5 text-[11px] font-mono border border-gray-200 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
                    />
                  {:else}
                    <input
                      type="text"
                      bind:value={field.default}
                      placeholder="default value"
                      class="w-full px-1.5 py-0.5 text-[11px] border border-gray-200 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
                    />
                  {/if}
                </div>
                {#if field.type === 'select'}
                  <div>
                    <span class="text-[9px] text-gray-400 dark:text-dark-text-muted">Options (one per line)</span>
                    <textarea
                      value={(field.options || []).join('\n')}
                      oninput={(e) => { field.options = (e.target as HTMLTextAreaElement).value.split('\n').map(s => s.trim()).filter(Boolean); }}
                      rows={3}
                      placeholder={"option1\noption2\noption3"}
                      class="w-full px-1.5 py-0.5 text-[11px] font-mono border border-gray-200 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text resize-y"
                    ></textarea>
                  </div>
                {/if}
              </div>
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  {/if}
</div>

<!-- Port descriptions -->
<div class="border-t border-gray-200 dark:border-dark-border pt-2 mt-2 space-y-2">
  <div>
    <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Output Ports</span>
    <div class="mt-1 space-y-1">
      <div>
        <span class="text-[11px] font-mono font-medium text-gray-700 dark:text-dark-text-secondary">data</span>
        <span class="text-[10px] text-gray-400 dark:text-dark-text-muted ml-1">— Workflow trigger inputs (pass-through)</span>
      </div>
    </div>
  </div>
</div>
