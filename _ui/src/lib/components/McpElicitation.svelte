<script lang="ts">
  import { onDestroy } from 'svelte';
  import { MessageCircleQuestion } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { answerMCPElicitation, listMCPElicitations, type ElicitAction, type MCPElicitation } from '@/lib/api/mcp-elicitations';
  import { elicitContent, elicitDefaults, elicitFields, watchElicitations } from '@/lib/helper/mcp-elicitation';

  interface Props {
    /** Watch for questions (a turn or tool call is running). */
    active: boolean;
    /** Only questions of this scope ("chats", "session:<id>", "developer:<id>"). */
    scope: string;
  }

  let { active, scope }: Props = $props();

  let items = $state<MCPElicitation[]>([]);
  let values = $state<Record<string, string | boolean>>({});
  let formFor = $state('');
  let sending = $state(false);
  let error = $state('');

  const watcher = watchElicitations((wait, signal) => listMCPElicitations(wait, signal), list => { items = list; });

  $effect(() => {
    if (active) watcher.start();
    else watcher.stop();
  });
  onDestroy(() => watcher.stop());

  let current = $derived(items.find(q => q.scope === scope) ?? null);
  let fields = $derived(current ? elicitFields(current.requested_schema) : []);

  $effect(() => {
    if (current && current.id !== formFor) {
      formFor = current.id;
      values = elicitDefaults(fields);
      error = '';
    }
  });

  async function respond(action: ElicitAction) {
    if (!current || sending) return;
    let content: Record<string, unknown> | undefined;
    if (action === 'accept') {
      const built = elicitContent(fields, values);
      if ('error' in built) { error = built.error; return; }
      content = built.content;
    }
    sending = true;
    const id = current.id;
    try {
      await answerMCPElicitation(id, action, content);
      items = items.filter(q => q.id !== id);
    } catch (e: any) {
      const message = e?.response?.data?.message || 'Could not send the answer';
      if (e?.response?.status === 404) items = items.filter(q => q.id !== id);
      if (e?.response?.status === 400) error = message;
      else addToast(message, 'alert');
    } finally {
      sending = false;
    }
  }
</script>

{#if current}
  <form
    class="border border-[var(--oc-violet)] bg-dark-surface text-xs"
    aria-label="Question from an MCP server"
    onsubmit={e => { e.preventDefault(); void respond('accept'); }}
  >
    <div class="flex items-center gap-2 border-b border-dark-border px-3 py-2">
      <MessageCircleQuestion size={14} class="shrink-0 text-[var(--oc-violet)]" />
      <span class="text-dark-text-muted">MCP server <span class="text-dark-text">{current.server || 'unknown'}</span> asks</span>
    </div>
    <div class="space-y-2 px-3 py-2">
      <p class="whitespace-pre-wrap text-sm text-dark-text">{current.message}</p>
      {#each fields as field (field.name)}
        <label class="block">
          {#if field.kind === 'boolean'}
            <span class="flex items-center gap-2">
              <input type="checkbox" checked={values[field.name] === true} onchange={e => (values[field.name] = e.currentTarget.checked)} />
              <span class="text-dark-text">{field.label}</span>
            </span>
          {:else}
            <span class="text-dark-text-secondary">{field.label}{field.required ? ' *' : ''}</span>
            {#if field.kind === 'select'}
              <select class="mt-1 block w-full border border-dark-border bg-dark-base px-2 py-1.5 text-sm text-dark-text" value={values[field.name]} onchange={e => (values[field.name] = e.currentTarget.value)}>
                <option value="">—</option>
                {#each field.options as option}
                  <option value={option.value}>{option.label}</option>
                {/each}
              </select>
            {:else}
              <input
                class="mt-1 block w-full border border-dark-border bg-dark-base px-2 py-1.5 text-sm text-dark-text"
                type={field.inputType}
                step={field.property.type === 'integer' ? '1' : 'any'}
                value={values[field.name]}
                oninput={e => (values[field.name] = e.currentTarget.value)}
              />
            {/if}
          {/if}
          {#if field.description}<span class="mt-0.5 block text-[11px] text-dark-text-muted">{field.description}</span>{/if}
        </label>
      {/each}
      {#if error}<p class="text-oc-red">{error}</p>{/if}
      <p class="text-[11px] text-dark-text-muted">Your answer goes to this MCP server only. Do not enter passwords or secrets unless you trust it.</p>
    </div>
    <div class="flex flex-wrap items-center gap-2 border-t border-dark-border px-3 py-2">
      <button type="submit" disabled={sending} class="bg-accent px-3 py-1.5 font-medium text-dark-base disabled:opacity-50">Send</button>
      <button type="button" disabled={sending} onclick={() => respond('decline')} class="border border-dark-border-subtle px-3 py-1.5 text-dark-text-secondary hover:bg-dark-elevated disabled:opacity-50">Decline</button>
      <button type="button" disabled={sending} onclick={() => respond('cancel')} class="px-3 py-1.5 text-dark-text-muted hover:bg-dark-elevated disabled:opacity-50">Cancel</button>
    </div>
  </form>
{/if}
