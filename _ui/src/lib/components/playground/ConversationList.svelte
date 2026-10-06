<script lang="ts">
  import { Check, GitBranch, Pencil, Trash2, X } from 'lucide-svelte';
  import type { PlaygroundConversation } from '@/lib/api/playground';

  interface Props {
    conversations: PlaygroundConversation[];
    activeId: string;
    loading: boolean;
    hasMore: boolean;
    /** True while an unsaved scratch buffer holds messages. */
    scratchDirty?: boolean;
    onSelect: (id: string) => void;
    onNew: () => void;
    onRename: (id: string, title: string) => void;
    onDelete: (id: string) => void;
    onLoadMore: () => void;
  }

  let {
    conversations,
    activeId,
    loading,
    hasMore,
    scratchDirty = false,
    onSelect,
    onNew,
    onRename,
    onDelete,
    onLoadMore,
  }: Props = $props();

  let editingId = $state('');
  let editingTitle = $state('');
  let confirmingId = $state('');

  function startRename(c: PlaygroundConversation) {
    confirmingId = '';
    editingId = c.id;
    editingTitle = c.title;
  }

  function commitRename() {
    const id = editingId;
    const title = editingTitle.trim();
    editingId = '';
    if (id && title) onRename(id, title);
  }

  function handleRenameKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') { e.preventDefault(); commitRename(); }
    if (e.key === 'Escape') { e.preventDefault(); editingId = ''; }
  }

  function handleScroll(e: Event) {
    const el = e.currentTarget as HTMLElement;
    if (!hasMore || loading) return;
    if (el.scrollTop + el.clientHeight >= el.scrollHeight - 48) onLoadMore();
  }

  function relativeTime(iso: string): string {
    const at = Date.parse(iso);
    if (Number.isNaN(at)) return '';
    const minutes = Math.round((Date.now() - at) / 60000);
    if (minutes < 1) return 'just now';
    if (minutes < 60) return `${minutes}m ago`;
    const hours = Math.round(minutes / 60);
    if (hours < 24) return `${hours}h ago`;
    const days = Math.round(hours / 24);
    if (days < 30) return `${days}d ago`;
    return new Date(at).toLocaleDateString();
  }

  const titleOf = (c: PlaygroundConversation) => c.title?.trim() || 'Untitled conversation';

  let query = $state('');

  function dayGroup(iso: string): string {
    const at = Date.parse(iso);
    if (Number.isNaN(at)) return 'Older';
    const today = new Date();
    today.setHours(0, 0, 0, 0);
    const days = Math.floor((today.getTime() - new Date(at).setHours(0, 0, 0, 0)) / 86400000);
    if (days <= 0) return 'Today';
    if (days === 1) return 'Yesterday';
    if (days < 7) return 'This week';
    if (days < 30) return 'This month';
    return 'Older';
  }

  // Filters the loaded page only; older conversations appear as they load.
  let groups = $derived.by(() => {
    const q = query.trim().toLowerCase();
    const out: Array<{ label: string; items: PlaygroundConversation[] }> = [];
    for (const c of conversations) {
      if (q && !titleOf(c).toLowerCase().includes(q)) continue;
      const label = dayGroup(c.updated_at);
      const last = out[out.length - 1];
      if (last?.label === label) last.items.push(c);
      else out.push({ label, items: [c] });
    }
    return out;
  });
</script>

<aside class="w-64 shrink-0 border-r border-dark-border bg-dark-base flex flex-col h-full">
  <div class="px-3 pt-3 pb-2 shrink-0 space-y-2">
    <button
      onclick={onNew}
      class="flex h-9 w-full items-center justify-between bg-dark-surface px-3 text-dark-text hover:bg-dark-elevated focus-visible:outline-1 focus-visible:outline-accent"
    >
      <span>+ new chat</span>
    </button>
    <label class="flex items-center gap-[1ch] border-b border-dark-border px-1 py-1.5 text-dark-text-muted">
      <span aria-hidden="true">/</span>
      <input
        bind:value={query}
        placeholder="search chats"
        aria-label="Search chats"
        class="min-w-0 flex-1 bg-transparent text-dark-text placeholder:text-dark-text-muted focus:outline-none"
      />
    </label>
  </div>

  <nav
    class="flex-1 min-h-0 overflow-y-auto pb-3"
    aria-label="Chats conversations"
    onscroll={handleScroll}
  >
    {#if scratchDirty && !activeId}
      <p class="mx-3 my-2 border-l border-[var(--oc-peach)] bg-dark-surface px-3 py-1.5 text-[var(--oc-peach)]">
        unsaved scratch — sending a message saves it
      </p>
    {/if}

    {#each groups as group (group.label)}
      <div class="px-4 pt-3 pb-1 text-dark-text-faint">{group.label}</div>
      {#each group.items as c (c.id)}
        <div class="group relative">
          {#if editingId === c.id}
            <div class="flex items-center gap-1 px-3 py-1">
              <!-- svelte-ignore a11y_autofocus -->
              <input
                bind:value={editingTitle}
                onkeydown={handleRenameKeydown}
                autofocus
                aria-label="Conversation title"
                class="flex-1 min-w-0 border-b border-accent bg-transparent px-1 py-0.5 text-dark-text focus:outline-none"
              />
              <button onclick={commitRename} aria-label="Save title" class="p-1 text-dark-text-muted hover:text-[var(--oc-green)] focus-visible:outline-1 focus-visible:outline-accent">
                <Check size={12} />
              </button>
              <button onclick={() => (editingId = '')} aria-label="Cancel rename" class="p-1 text-dark-text-muted hover:text-dark-text focus-visible:outline-1 focus-visible:outline-accent">
                <X size={12} />
              </button>
            </div>
          {:else if confirmingId === c.id}
            <div class="mx-3 my-1 border-l border-[var(--oc-red)] bg-dark-surface px-3 py-2">
              <div class="mb-1.5 leading-snug text-[var(--oc-red)]">delete “{titleOf(c)}” and its transcript?</div>
              <div class="flex gap-[2ch]">
                <button onclick={() => { confirmingId = ''; onDelete(c.id); }} class="text-[var(--oc-red)] underline-offset-4 hover:underline focus-visible:outline-1 focus-visible:outline-accent">delete</button>
                <button onclick={() => (confirmingId = '')} class="text-dark-text-muted hover:text-dark-text focus-visible:outline-1 focus-visible:outline-accent">cancel</button>
              </div>
            </div>
          {:else}
            <button
              onclick={() => onSelect(c.id)}
              aria-current={activeId === c.id ? 'page' : undefined}
              title={`${titleOf(c)} · ${relativeTime(c.updated_at)}`}
              class={['flex w-full items-center gap-[1ch] border-l-2 py-1 pl-3.5 pr-12 text-left focus-visible:outline-1 focus-visible:-outline-offset-1 focus-visible:outline-accent', activeId === c.id ? 'border-accent bg-dark-surface text-dark-text' : 'border-transparent text-dark-text-muted hover:bg-dark-surface hover:text-dark-text']}
            >
              {#if c.forked_from_sequence}
                <GitBranch size={11} class="shrink-0 text-[var(--oc-violet)]" />
              {:else}
                <span class="shrink-0 text-dark-text-faint" aria-hidden="true">#</span>
              {/if}
              <span class="truncate">{titleOf(c)}</span>
            </button>
            <div class="absolute right-2 top-1/2 flex -translate-y-1/2 opacity-0 group-hover:opacity-100 group-focus-within:opacity-100">
              <button onclick={() => startRename(c)} aria-label={`Rename ${titleOf(c)}`} class="p-1 text-dark-text-muted hover:text-dark-text focus-visible:opacity-100 focus-visible:outline-1 focus-visible:outline-accent">
                <Pencil size={11} />
              </button>
              <button onclick={() => { editingId = ''; confirmingId = c.id; }} aria-label={`Delete ${titleOf(c)}`} class="p-1 text-dark-text-muted hover:text-[var(--oc-red)] focus-visible:opacity-100 focus-visible:outline-1 focus-visible:outline-accent">
                <Trash2 size={11} />
              </button>
            </div>
          {/if}
        </div>
      {/each}
    {/each}

    {#if loading}
      <p class="px-4 py-3 text-dark-text-muted">loading…</p>
    {:else if conversations.length === 0}
      <p class="px-4 py-4 leading-relaxed text-dark-text-muted">No saved conversations yet. Send a message to start one.</p>
    {:else if groups.length === 0}
      <p class="px-4 py-4 text-dark-text-muted">no loaded chats match “{query}”</p>
    {/if}
    {#if !loading && hasMore}
      <button onclick={onLoadMore} class="w-full px-4 py-2 text-left text-dark-text-muted hover:text-dark-text focus-visible:outline-1 focus-visible:outline-accent">
        load older conversations
      </button>
    {/if}
  </nav>
</aside>
