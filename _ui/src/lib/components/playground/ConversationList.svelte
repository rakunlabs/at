<script lang="ts">
  import { Check, GitBranch, Loader2, Pencil, Plus, Trash2, X } from 'lucide-svelte';
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
</script>

<aside class="w-60 shrink-0 border-r border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface flex flex-col h-full">
  <div class="px-2 py-2 border-b border-gray-200 dark:border-dark-border shrink-0">
    <button
      onclick={onNew}
      class="w-full flex items-center justify-center gap-1.5 px-2 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent transition-colors"
    >
      <Plus size={13} />
      New conversation
    </button>
  </div>

  <nav
    class="flex-1 min-h-0 overflow-y-auto px-1.5 py-1.5 space-y-0.5"
    aria-label="Playground conversations"
    onscroll={handleScroll}
  >
    {#if scratchDirty && !activeId}
      <div class="px-2 py-2 text-[11px] text-amber-700 dark:text-amber-400 border border-amber-300 dark:border-amber-900/60 bg-amber-50 dark:bg-amber-900/10">
        Unsaved scratch buffer — sending a message saves it.
      </div>
    {/if}

    {#each conversations as c (c.id)}
      <div class="group relative">
        {#if editingId === c.id}
          <div class="flex items-center gap-1 px-1.5 py-1">
            <!-- svelte-ignore a11y_autofocus -->
            <input
              bind:value={editingTitle}
              onkeydown={handleRenameKeydown}
              autofocus
              aria-label="Conversation title"
              class="flex-1 min-w-0 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated dark:text-dark-text px-1.5 py-1 text-xs focus:outline-none focus:ring-2 focus:ring-gray-900/10 focus:border-gray-400 transition-colors"
            />
            <button
              onclick={commitRename}
              aria-label="Save title"
              class="p-1 text-gray-500 dark:text-dark-text-muted hover:text-green-600 dark:hover:text-green-400 focus-visible:outline-2 focus-visible:outline-accent transition-colors"
            >
              <Check size={12} />
            </button>
            <button
              onclick={() => (editingId = '')}
              aria-label="Cancel rename"
              class="p-1 text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent transition-colors"
            >
              <X size={12} />
            </button>
          </div>
        {:else if confirmingId === c.id}
          <div class="px-2 py-1.5 border border-red-300 dark:border-red-900/60 bg-red-50 dark:bg-red-900/10">
            <div class="text-[11px] text-red-700 dark:text-red-400 mb-1.5 leading-snug">Delete “{titleOf(c)}” and its transcript?</div>
            <div class="flex gap-1.5">
              <button
                onclick={() => { confirmingId = ''; onDelete(c.id); }}
                class="px-2 py-0.5 text-[11px] bg-red-600 text-white hover:bg-red-700 focus-visible:outline-2 focus-visible:outline-accent transition-colors"
              >
                Delete
              </button>
              <button
                onclick={() => (confirmingId = '')}
                class="px-2 py-0.5 text-[11px] border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-secondary hover:bg-white dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent transition-colors"
              >
                Cancel
              </button>
            </div>
          </div>
        {:else}
          <button
            onclick={() => onSelect(c.id)}
            aria-current={activeId === c.id ? 'page' : undefined}
            class={['w-full text-left px-2 py-1.5 pr-12 focus-visible:outline-2 focus-visible:outline-accent transition-colors', activeId === c.id ? 'bg-gray-100 dark:bg-dark-elevated' : 'hover:bg-gray-50 dark:hover:bg-dark-elevated/60']}
          >
            <div class="flex items-center gap-1">
              {#if c.forked_from_sequence}
                <GitBranch size={11} class="shrink-0 text-purple-500 dark:text-purple-400" />
              {/if}
              <span class={['truncate text-xs', activeId === c.id ? 'font-semibold text-gray-900 dark:text-dark-text' : 'text-gray-700 dark:text-dark-text-secondary']}>{titleOf(c)}</span>
            </div>
            <div class="text-[10px] text-gray-400 dark:text-dark-text-muted mt-0.5">{relativeTime(c.updated_at)}</div>
          </button>
          <div class="absolute right-1 top-1 flex opacity-0 group-hover:opacity-100 group-focus-within:opacity-100 transition-opacity">
            <button
              onclick={() => startRename(c)}
              aria-label={`Rename ${titleOf(c)}`}
              class="p-1 text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text focus-visible:opacity-100 focus-visible:outline-2 focus-visible:outline-accent transition-colors"
            >
              <Pencil size={11} />
            </button>
            <button
              onclick={() => { editingId = ''; confirmingId = c.id; }}
              aria-label={`Delete ${titleOf(c)}`}
              class="p-1 text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 focus-visible:opacity-100 focus-visible:outline-2 focus-visible:outline-accent transition-colors"
            >
              <Trash2 size={11} />
            </button>
          </div>
        {/if}
      </div>
    {/each}

    {#if loading}
      <div class="flex items-center justify-center gap-1.5 py-3 text-[11px] text-gray-400 dark:text-dark-text-muted">
        <Loader2 size={12} class="animate-spin" />
        Loading
      </div>
    {:else if conversations.length === 0}
      <p class="px-2 py-4 text-[11px] text-gray-400 dark:text-dark-text-muted leading-relaxed">
        No saved conversations yet. Send a message to start one.
      </p>
    {:else if hasMore}
      <button
        onclick={onLoadMore}
        class="w-full px-2 py-1.5 text-[11px] text-gray-500 dark:text-dark-text-muted hover:text-gray-800 dark:hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent transition-colors"
      >
        Load older conversations
      </button>
    {/if}
  </nav>
</aside>
