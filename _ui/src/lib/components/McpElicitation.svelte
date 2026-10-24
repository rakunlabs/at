<script lang="ts">
  import { onDestroy } from 'svelte';
  import { ExternalLink, MessageCircleQuestion, TriangleAlert } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { answerMCPElicitation, listMCPElicitations, type ElicitAction, type MCPElicitation } from '@/lib/api/mcp-elicitations';
  import { elicitContent, elicitDefaults, elicitFields, openElicitationURL, urlParts, watchElicitations } from '@/lib/helper/mcp-elicitation';

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
  let fields = $derived(current && current.mode !== 'url' && current.requested_schema ? elicitFields(current.requested_schema) : []);
  let link = $derived(current?.mode === 'url' && current.url ? urlParts(current.url) : null);
  /** A required URL was opened; the next step is confirming it is finished. */
  let opened = $state('');

  $effect(() => {
    if (current && current.id !== formFor) {
      formFor = current.id;
      values = elicitDefaults(fields);
      error = '';
    }
  });

  /**
   * URL mode: opening is the consent. For an elicitation the server sent
   * mid-call, that answers accept at once; for a required one (-32042) the
   * call is retried after the person says the page is finished.
   */
  async function openLink() {
    if (!current?.url) return;
    if (!openElicitationURL(current.url)) { error = 'This link cannot be opened.'; return; }
    if (current.required) opened = current.id;
    else await respond('accept');
  }

  async function respond(action: ElicitAction) {
    if (!current || sending) return;
    let content: Record<string, unknown> | undefined;
    if (action === 'accept' && current.mode !== 'url') {
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
    onsubmit={e => { e.preventDefault(); if (current?.mode !== 'url') void respond('accept'); }}
  >
    <div class="flex items-center gap-2 border-b border-dark-border px-3 py-2">
      <MessageCircleQuestion size={14} class="shrink-0 text-[var(--oc-violet)]" />
      <span class="text-dark-text-muted">MCP server <span class="text-dark-text">{current.server || 'unknown'}</span> asks</span>
    </div>
    <div class="space-y-2 px-3 py-2">
      <p class="whitespace-pre-wrap text-sm text-dark-text">{current.message}</p>
      {#if current.mode === 'url'}
        {#if link}
          <div class="border border-dark-border bg-dark-base px-2 py-1.5">
            <p class="text-[11px] text-dark-text-muted">Opens a page on <span class="font-semibold text-oc-peach">{link.host}</span></p>
            <p class="mt-0.5 break-all font-mono text-[11px] text-dark-text-secondary">{link.before}<span class="font-semibold text-dark-text">{link.host}</span>{link.after}</p>
          </div>
          {#if current.punycode}
            <p class="flex items-start gap-1.5 text-oc-red"><TriangleAlert size={13} class="mt-0.5 shrink-0" />This address uses international characters (Punycode) and may imitate another site. Check it carefully.</p>
          {/if}
          <p class="text-[11px] text-dark-text-muted">The page opens in a new tab. What you enter there goes to that site, not to AT or the model.</p>
        {:else}
          <p class="text-oc-red">The server sent an address that cannot be opened.</p>
        {/if}
      {/if}
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
      {#if current.mode !== 'url'}
        <p class="text-[11px] text-dark-text-muted">Your answer goes to this MCP server only. Do not enter passwords or secrets unless you trust it.</p>
      {/if}
    </div>
    <div class="flex flex-wrap items-center gap-2 border-t border-dark-border px-3 py-2">
      {#if current.mode === 'url'}
        {#if opened === current.id}
          <button type="button" disabled={sending} onclick={() => respond('accept')} class="bg-accent px-3 py-1.5 font-medium text-dark-base disabled:opacity-50">I finished, continue</button>
          <button type="button" disabled={sending || !link} onclick={openLink} class="inline-flex items-center gap-1 border border-dark-border-subtle px-3 py-1.5 text-dark-text-secondary hover:bg-dark-elevated disabled:opacity-50"><ExternalLink size={12} /> Open again</button>
        {:else}
          <button type="button" disabled={sending || !link} onclick={openLink} class="inline-flex items-center gap-1 bg-accent px-3 py-1.5 font-medium text-dark-base disabled:opacity-50"><ExternalLink size={12} /> Open {link?.host ?? 'link'}</button>
        {/if}
      {:else}
        <button type="submit" disabled={sending} class="bg-accent px-3 py-1.5 font-medium text-dark-base disabled:opacity-50">Send</button>
      {/if}
      <button type="button" disabled={sending} onclick={() => respond('decline')} class="border border-dark-border-subtle px-3 py-1.5 text-dark-text-secondary hover:bg-dark-elevated disabled:opacity-50">Decline</button>
      <button type="button" disabled={sending} onclick={() => respond('cancel')} class="px-3 py-1.5 text-dark-text-muted hover:bg-dark-elevated disabled:opacity-50">Cancel</button>
    </div>
  </form>
{/if}
