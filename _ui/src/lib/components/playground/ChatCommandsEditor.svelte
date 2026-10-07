<script lang="ts">
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    createWorkspaceChatCommand,
    deleteWorkspaceChatCommand,
    saveChatCommands,
    updateWorkspaceChatCommand,
    type ChatCommand,
  } from '@/lib/api/chat-commands';

  interface Props {
    personal: ChatCommand[];
    workspace: ChatCommand[];
    models: string[];
    /** Built-in names a custom command may not take. */
    reserved: string[];
    onchange: () => void | Promise<void>;
  }
  let { personal, workspace, models, reserved, onchange }: Props = $props();

  interface Draft { id: string; scope: 'personal' | 'workspace'; name: string; description: string; template: string; model: string }

  const empty = (): Draft => ({ id: '', scope: 'personal', name: '', description: '', template: '', model: '' });
  let draft = $state<Draft | null>(null);
  let saving = $state(false);
  let confirmDelete = $state('');

  let nameProblem = $derived.by(() => {
    if (!draft) return '';
    const name = draft.name.trim().replace(/^\//, '').toLowerCase();
    if (!name) return '';
    if (!/^[a-z0-9][a-z0-9._-]{0,31}$/.test(name)) return 'Use 1–32 lowercase letters, digits, ".", "_" or "-".';
    if (reserved.includes(name)) return `/${name} is a built-in command.`;
    const list = draft.scope === 'personal' ? personal : workspace;
    if (list.some(c => c.name === name && c.id !== draft!.id)) return `/${name} already exists here.`;
    return '';
  });

  function edit(c: ChatCommand) {
    confirmDelete = '';
    draft = { id: c.id, scope: c.scope, name: c.name, description: c.description ?? '', template: c.template, model: c.model ?? '' };
  }

  async function save() {
    if (!draft || saving || nameProblem) return;
    const input = { name: draft.name.trim().replace(/^\//, '').toLowerCase(), description: draft.description.trim(), template: draft.template, model: draft.model };
    saving = true;
    try {
      if (draft.scope === 'personal') {
        const rest = personal.filter(c => c.id !== draft!.id);
        const next = draft.id ? personal.map(c => (c.id === draft!.id ? { ...c, ...input } : c)) : [...rest, { id: '', ...input }];
        await saveChatCommands(next);
      } else if (draft.id) {
        await updateWorkspaceChatCommand(draft.id, input);
      } else {
        await createWorkspaceChatCommand(input);
      }
      addToast(`Saved /${input.name}.`, 'info');
      draft = null;
      await onchange();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save the command', 'alert');
    } finally {
      saving = false;
    }
  }

  async function remove(c: ChatCommand) {
    if (confirmDelete !== c.id) { confirmDelete = c.id; return; }
    confirmDelete = '';
    saving = true;
    try {
      if (c.scope === 'personal') await saveChatCommands(personal.filter(p => p.id !== c.id));
      else await deleteWorkspaceChatCommand(c.id);
      if (draft?.id === c.id) draft = null;
      await onchange();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete the command', 'alert');
    } finally {
      saving = false;
    }
  }

  const inputClass = 'block w-full border border-dark-border-subtle bg-dark-elevated px-3 py-1.5 text-sm text-dark-text placeholder:text-dark-text-muted focus:outline-none focus:border-accent';
</script>

<div class="max-w-3xl space-y-4">
  <div>
    <h3 class="text-xs font-medium text-dark-text">Commands</h3>
    <p class="mt-0.5 text-dark-text-muted">
      A command is a saved prompt you run by typing <code class="text-[var(--oc-peach)]">/name</code> in the composer.
      <code class="text-[var(--oc-peach)]">$ARGUMENTS</code> is replaced by everything typed after the name, <code class="text-[var(--oc-peach)]">$1</code>…<code class="text-[var(--oc-peach)]">$9</code> by single arguments (quote to group words).
      Without a placeholder, the arguments are added at the end.
    </p>
  </div>

  {#snippet list(label: string, items: ChatCommand[])}
    <section aria-label={label}>
      <h4 class="mb-1 text-dark-text-muted">{label}</h4>
      {#if items.length === 0}
        <p class="text-dark-text-faint">None yet.</p>
      {:else}
        <ul class="divide-y divide-dark-border border-y border-dark-border">
          {#each items as c (c.id)}
            <li class="flex items-baseline gap-[2ch] py-1.5">
              <span class="shrink-0 text-dark-text">/{c.name}</span>
              <span class="min-w-0 flex-1 truncate text-dark-text-muted" title={c.template}>{c.description || c.template.split('\n')[0]}</span>
              {#if c.model}<span class="hidden shrink-0 text-dark-text-faint sm:inline">{c.model}</span>{/if}
              {#if c.can_edit}
                <button onclick={() => edit(c)} disabled={saving} class="oc-link shrink-0 focus-visible:outline-1 focus-visible:outline-accent">edit</button>
                <button onclick={() => remove(c)} disabled={saving} class={['shrink-0 focus-visible:outline-1 focus-visible:outline-accent', confirmDelete === c.id ? 'text-[var(--oc-red)]' : 'oc-link']}>{confirmDelete === c.id ? 'confirm delete' : 'delete'}</button>
              {:else}
                <span class="shrink-0 text-dark-text-faint" title="Only the member who shared it can change it">shared</span>
              {/if}
            </li>
          {/each}
        </ul>
      {/if}
    </section>
  {/snippet}

  {@render list('Only me', personal)}
  {@render list('Workspace', workspace)}

  {#if draft}
    <form class="space-y-3 border border-dark-border p-4" onsubmit={e => { e.preventDefault(); void save(); }}>
      <div class="flex flex-wrap gap-3">
        <label class="min-w-0 flex-1 basis-48 text-dark-text-muted">Name
          <span class="mt-1 flex items-center border border-dark-border-subtle bg-dark-elevated focus-within:border-accent">
            <span class="pl-3 text-dark-text-faint">/</span>
            <input bind:value={draft.name} required maxlength={33} placeholder="review" spellcheck="false" autocomplete="off" class="min-w-0 flex-1 bg-transparent py-1.5 pr-3 pl-0.5 text-sm text-dark-text placeholder:text-dark-text-muted focus:outline-none" />
          </span>
        </label>
        <label class="basis-40 text-dark-text-muted">Visible to
          <select bind:value={draft.scope} disabled={!!draft.id} class={`${inputClass} mt-1`}>
            <option value="personal">Only me</option>
            <option value="workspace">Workspace</option>
          </select>
        </label>
      </div>
      {#if nameProblem}<p role="alert" class="text-[var(--oc-red)]">{nameProblem}</p>{/if}
      <label class="block text-dark-text-muted">Description
        <input bind:value={draft.description} maxlength={200} placeholder="Shown next to the name in the command list" class={`${inputClass} mt-1`} />
      </label>
      <label class="block text-dark-text-muted">Prompt
        <textarea bind:value={draft.template} required rows={6} placeholder={'Review the following change for bugs and missing tests:\n\n$ARGUMENTS'} class={`${inputClass} mt-1 resize-y font-mono`}></textarea>
      </label>
      <label class="block text-dark-text-muted">Model
        <select bind:value={draft.model} class={`${inputClass} mt-1`}>
          <option value="">The conversation's model</option>
          {#each models as model}<option value={model}>{model}</option>{/each}
        </select>
        <span class="mt-1 block text-dark-text-faint">A chosen model answers this one message; the conversation then continues on its own model.</span>
      </label>
      <div class="flex gap-2">
        <button type="submit" disabled={saving || !draft.name.trim() || !draft.template.trim() || !!nameProblem} class="bg-accent px-3 py-1.5 text-sm text-dark-base hover:bg-accent-hover disabled:opacity-30">{draft.id ? 'Save changes' : draft.scope === 'workspace' ? 'Share command' : 'Save command'}</button>
        <button type="button" onclick={() => (draft = null)} class="border border-dark-border-subtle px-3 py-1.5 text-sm text-dark-text-secondary hover:bg-dark-elevated">Cancel</button>
      </div>
    </form>
  {:else}
    <button onclick={() => { confirmDelete = ''; draft = empty(); }} class="border border-dark-border-subtle px-3 py-1.5 text-sm text-dark-text-secondary hover:bg-dark-elevated">New command</button>
  {/if}
</div>
