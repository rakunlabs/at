<script lang="ts">
  import {
    listCommentsByTask,
    createComment,
    updateComment,
    deleteComment,
    type IssueComment,
  } from '@/lib/api/issue-comments';
  import { addToast } from '@/lib/store/toast.svelte';
  import { formatDate } from '@/lib/helper/format';
  import { Bot, User, Settings, Send, Pencil, Trash2, X, Check, MessageSquare, Reply } from 'lucide-svelte';

  interface Props {
    taskId: string;
  }

  let { taskId }: Props = $props();

  let comments = $state<IssueComment[]>([]);
  let loading = $state(true);
  let newBody = $state('');
  let submitting = $state(false);
  let editingId = $state<string | null>(null);
  let editBody = $state('');
  let replyToId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);

  // Load comments
  async function load() {
    loading = true;
    try {
      const data = await listCommentsByTask(taskId);
      comments = data || [];
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load comments', 'alert');
    } finally {
      loading = false;
    }
  }

  load();

  // Reload when taskId changes
  $effect(() => {
    if (taskId) load();
  });

  // Build thread structure: top-level + replies
  interface CommentThread {
    comment: IssueComment;
    replies: IssueComment[];
  }

  let threads = $derived.by(() => {
    const topLevel: CommentThread[] = [];
    const replyMap = new Map<string, IssueComment[]>();

    for (const c of comments) {
      if (c.parent_id) {
        if (!replyMap.has(c.parent_id)) replyMap.set(c.parent_id, []);
        replyMap.get(c.parent_id)!.push(c);
      } else {
        topLevel.push({ comment: c, replies: [] });
      }
    }

    // Attach replies
    for (const thread of topLevel) {
      thread.replies = replyMap.get(thread.comment.id) || [];
      // Sort replies by creation time
      thread.replies.sort((a, b) => a.created_at.localeCompare(b.created_at));
    }

    // Sort threads by creation time (newest last)
    topLevel.sort((a, b) => a.comment.created_at.localeCompare(b.comment.created_at));

    return topLevel;
  });

  // Author type display
  function authorIcon(type: string) {
    switch (type) {
      case 'agent': return Bot;
      case 'user': return User;
      case 'system': return Settings;
      default: return User;
    }
  }

  function authorBadgeClass(type: string): string {
    switch (type) {
      case 'agent': return 'bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-400';
      case 'user': return 'bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-400';
      case 'system': return 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted';
      default: return 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted';
    }
  }

  // Create comment
  async function handleCreate() {
    if (!newBody.trim()) return;

    submitting = true;
    try {
      const data: Partial<IssueComment> = {
        body: newBody.trim(),
        author_type: 'user',
      };
      if (replyToId) data.parent_id = replyToId;
      await createComment(taskId, data);
      newBody = '';
      replyToId = null;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to add comment', 'alert');
    } finally {
      submitting = false;
    }
  }

  // Edit
  function startEdit(comment: IssueComment) {
    editingId = comment.id;
    editBody = comment.body;
  }

  function cancelEdit() {
    editingId = null;
    editBody = '';
  }

  async function saveEdit() {
    if (!editingId || !editBody.trim()) return;
    try {
      await updateComment(editingId, { body: editBody.trim() });
      cancelEdit();
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update comment', 'alert');
    }
  }

  // Delete
  async function handleDelete(id: string) {
    try {
      await deleteComment(id);
      deleteConfirm = null;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete comment', 'alert');
    }
  }

  function startReply(commentId: string) {
    replyToId = commentId;
    newBody = '';
  }

  function cancelReply() {
    replyToId = null;
  }
</script>

<div class="space-y-4">
  {#if loading}
    <div class="text-sm text-gray-400 dark:text-dark-text-muted py-4 text-center">Loading comments...</div>
  {:else if threads.length === 0}
    <div class="text-sm text-gray-400 dark:text-dark-text-muted py-8 text-center flex flex-col items-center gap-2">
      <MessageSquare size={20} class="text-gray-300 dark:text-dark-text-faint" />
      <span>No comments yet</span>
    </div>
  {:else}
    {#each threads as thread}
      <!-- Top-level comment -->
      {@render commentBlock(thread.comment, false)}
      <!-- Replies -->
      {#if thread.replies.length > 0}
        <div class="ml-6 border-l-2 border-gray-100 dark:border-dark-border pl-4 space-y-3">
          {#each thread.replies as reply}
            {@render commentBlock(reply, true)}
          {/each}
        </div>
      {/if}
    {/each}
  {/if}

  <!-- Reply indicator -->
  {#if replyToId}
    <div class="flex items-center gap-2 text-xs text-gray-500 dark:text-dark-text-muted bg-gray-50 dark:bg-dark-base px-3 py-1.5 border border-gray-200 dark:border-dark-border">
      <Reply size={12} />
      <span>Replying to comment</span>
      <button onclick={cancelReply} class="ml-auto p-0.5 hover:text-gray-700 dark:hover:text-dark-text-secondary">
        <X size={12} />
      </button>
    </div>
  {/if}

  <!-- New comment form -->
  <form onsubmit={(e) => { e.preventDefault(); handleCreate(); }} class="flex gap-2">
    <textarea
      bind:value={newBody}
      placeholder="Add a comment..."
      rows="2"
      class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text transition-colors resize-y"
    ></textarea>
    <button
      type="submit"
      disabled={submitting || !newBody.trim()}
      class="self-end p-2 bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-40"
      title="Send"
    >
      <Send size={16} />
    </button>
  </form>
</div>

{#snippet commentBlock(comment: IssueComment, isReply: boolean)}
  <div class="group">
    <!-- Header -->
    <div class="flex items-center gap-2 mb-1">
      <svelte:component this={authorIcon(comment.author_type)} size={12} class="text-gray-400 dark:text-dark-text-muted" />
      <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">
        {comment.author_id || 'Anonymous'}
      </span>
      <span class="inline-block px-1.5 py-0 text-[10px] font-medium capitalize {authorBadgeClass(comment.author_type)}">
        {comment.author_type}
      </span>
      <span class="text-[10px] text-gray-400 dark:text-dark-text-muted">{formatDate(comment.created_at)}</span>
      {#if comment.updated_at !== comment.created_at}
        <span class="text-[10px] text-gray-400 dark:text-dark-text-muted italic">edited</span>
      {/if}

      <!-- Actions (show on hover) -->
      <div class="ml-auto flex items-center gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
        {#if !isReply}
          <button onclick={() => startReply(comment.id)}
            class="p-1 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors" title="Reply">
            <Reply size={12} />
          </button>
        {/if}
        <button onclick={() => startEdit(comment)}
          class="p-1 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors" title="Edit">
          <Pencil size={12} />
        </button>
        {#if deleteConfirm === comment.id}
          <button onclick={() => handleDelete(comment.id)}
            class="px-1.5 py-0.5 text-[10px] bg-red-600 text-white hover:bg-red-700">Confirm</button>
          <button onclick={() => (deleteConfirm = null)}
            class="px-1.5 py-0.5 text-[10px] border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated">Cancel</button>
        {:else}
          <button onclick={() => (deleteConfirm = comment.id)}
            class="p-1 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 transition-colors" title="Delete">
            <Trash2 size={12} />
          </button>
        {/if}
      </div>
    </div>

    <!-- Body (editable or display) -->
    {#if editingId === comment.id}
      <div class="flex gap-2">
        <textarea bind:value={editBody} rows="2"
          class="flex-1 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm dark:bg-dark-elevated dark:text-dark-text transition-colors resize-y"></textarea>
        <div class="flex flex-col gap-1 self-end">
          <button onclick={saveEdit} class="p-1 bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors" title="Save">
            <Check size={14} />
          </button>
          <button onclick={cancelEdit} class="p-1 border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-500 transition-colors" title="Cancel">
            <X size={14} />
          </button>
        </div>
      </div>
    {:else}
      <div class="text-sm text-gray-700 dark:text-dark-text-secondary whitespace-pre-wrap leading-relaxed">
        {comment.body}
      </div>
    {/if}
  </div>
{/snippet}
