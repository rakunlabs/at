<script lang="ts">
  import { literalError } from '@/lib/workflow/data-operations';
  let { config }: { config: Record<string, any> } = $props();
  const id = $props.id();
  let error = $derived(literalError(config));
  function changeType(kind: string) {
    config.value_type = kind;
    config.value = kind === 'number' ? '0' : kind === 'boolean' ? 'true' : kind === 'json' ? '{}' : '';
  }
</script>

<div class="space-y-2">
  <div><label for={`${id}-type`} class="block text-xs text-gray-600 dark:text-dark-text-secondary">Value type</label>
    <select id={`${id}-type`} value={config.value_type || 'string'} onchange={event => changeType(event.currentTarget.value)} class="mt-1 w-full border border-gray-300 bg-white p-2 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text">
      <option value="string">Text</option><option value="number">Number</option><option value="boolean">Boolean</option><option value="null">Null</option><option value="json">JSON object / array / value</option>
    </select>
  </div>
  {#if config.value_type !== 'null'}
    <div><label for={`${id}-value`} class="block text-xs text-gray-600 dark:text-dark-text-secondary">Value</label>
      {#if config.value_type === 'json'}
        <textarea id={`${id}-value`} bind:value={config.value} rows="3" aria-invalid={!!error} class="mt-1 w-full border border-gray-300 bg-white p-2 font-mono text-xs dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text"></textarea>
      {:else if config.value_type === 'boolean'}
        <select id={`${id}-value`} bind:value={config.value} class="mt-1 w-full border border-gray-300 bg-white p-2 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text"><option value="true">true</option><option value="false">false</option></select>
      {:else}
        <input id={`${id}-value`} bind:value={config.value} inputmode={config.value_type === 'number' ? 'decimal' : 'text'} aria-invalid={!!error} class="mt-1 w-full border border-gray-300 bg-white p-2 text-xs dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text" />
      {/if}
    </div>
  {/if}
  {#if error}<p class="text-xs text-red-700 dark:text-red-400">{error}</p>{/if}
</div>
