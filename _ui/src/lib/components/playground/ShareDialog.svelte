<script lang="ts">
  import { Check, Clipboard, Eye, Loader2, RefreshCw, Share2, Trash2, X } from 'lucide-svelte';

  import {
    getConversationShare,
    previewChatShare,
    publishChatShare,
    revokeChatShare,
    updateChatShare,
    type ChatShare,
    type ChatShareOptions,
    type ChatSharePreview,
  } from '@/lib/api/playground';
  import { listWorkspaces, type Workspace } from '@/lib/api/workspaces';
  import { workspaceTransport } from '@/lib/api/transport';
  import { addToast } from '@/lib/store/toast.svelte';
  import { onMount } from 'svelte';

  interface Boundary { sequence: number; label: string }
  interface Props { conversationId: string; boundaries: Boundary[]; onclose: () => void }
  let { conversationId, boundaries, onclose }: Props = $props();

  let workspaces = $state<Workspace[]>([]);
  let workspaceId = $state(workspaceTransport.selected);
  let throughSequence = $state(0);
  let options = $state<ChatShareOptions>({ include_system_prompt: false, include_tool_outputs: false, include_attachments: false });
  let share = $state<ChatShare | null>(null);
  let preview = $state<ChatSharePreview | null>(null);
  let loading = $state(true);
  let working = $state(false);
  let confirmRevoke = $state(false);
  let panel: HTMLDivElement;

  onMount(() => panel?.focus());

  $effect(() => {
    if (!throughSequence && boundaries.length > 0) throughSequence = boundaries.at(-1)?.sequence ?? 0;
    void loadWorkspaceShare(workspaceId);
  });

  async function loadWorkspaceShare(target: string) {
    if (!target) return;
    loading = true;
    preview = null;
    try {
      if (workspaces.length === 0) workspaces = (await listWorkspaces()).filter(w => !w.archived);
      const existing = await getConversationShare(conversationId, target);
      share = existing;
      if (existing) {
        throughSequence = existing.through_sequence;
        options = { ...existing.options };
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load sharing state', 'alert');
    } finally {
      loading = false;
    }
  }

  async function buildPreview() {
    if (!throughSequence) return;
    working = true;
    try {
      preview = await previewChatShare(conversationId, throughSequence, options, workspaceId);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to preview the snapshot', 'alert');
    } finally {
      working = false;
    }
  }

  async function saveShare() {
    if (!throughSequence) return;
    working = true;
    try {
      share = share
        ? await updateChatShare(share.id, throughSequence, options, workspaceId)
        : await publishChatShare(conversationId, throughSequence, options, workspaceId);
      preview = null;
      addToast(share.version > 1 ? 'Shared snapshot updated' : 'Conversation shared with the workspace', 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to publish the snapshot', 'alert');
    } finally {
      working = false;
    }
  }

  async function copyLink() {
    if (!share) return;
    const target = new URL(location.href);
    target.hash = `/chats/shared/${encodeURIComponent(share.id)}`;
    target.searchParams.set('workspace_id', share.workspace_id);
    const link = target.toString();
    try {
      await navigator.clipboard.writeText(link);
      addToast('Share link copied', 'info');
    } catch {
      addToast(`Copy this link: ${link}`, 'alert');
    }
  }

  async function revoke() {
    if (!share) return;
    working = true;
    try {
      await revokeChatShare(share.id, workspaceId);
      share = null;
      preview = null;
      confirmRevoke = false;
      addToast('Share revoked. Existing copies are unchanged.', 'info');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to revoke the share', 'alert');
    } finally {
      working = false;
    }
  }
</script>

<div class="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" role="presentation" onclick={(e) => { if (e.currentTarget === e.target) onclose(); }}>
  <div bind:this={panel} role="dialog" aria-modal="true" aria-labelledby="share-dialog-title" tabindex="-1" onkeydown={(e) => { if (e.key === 'Escape') onclose(); }} class="flex max-h-[88vh] w-full max-w-2xl flex-col border border-gray-200 bg-white dark:border-dark-border dark:bg-dark-surface focus:outline-none">
    <header class="flex items-start justify-between gap-4 border-b border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-border dark:bg-dark-base">
      <div>
        <h2 id="share-dialog-title" class="flex items-center gap-2 text-sm font-semibold text-gray-900 dark:text-dark-text"><Share2 size={15} /> Share conversation snapshot</h2>
        <p class="mt-1 text-[11px] leading-relaxed text-gray-500 dark:text-dark-text-muted">Publish a fixed, read-only prefix. Later messages stay private until you update it.</p>
      </div>
      <button onclick={onclose} aria-label="Close share dialog" class="p-1 text-gray-400 hover:bg-gray-200 hover:text-gray-700 focus-visible:outline-2 focus-visible:outline-accent dark:text-dark-text-muted dark:hover:bg-dark-elevated dark:hover:text-dark-text"><X size={16} /></button>
    </header>

    <div class="min-h-0 flex-1 overflow-y-auto p-4">
      {#if loading}
        <div class="flex min-h-40 items-center justify-center gap-2 text-xs text-gray-500 dark:text-dark-text-muted"><Loader2 size={14} class="animate-spin" /> Loading sharing state</div>
      {:else}
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="block">
            <span class="mb-1 block text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Workspace</span>
            <select bind:value={workspaceId} class="h-10 w-full border border-gray-300 bg-white px-2.5 text-xs dark:border-dark-border-subtle dark:bg-dark-surface dark:text-dark-text-secondary focus-visible:outline-2 focus-visible:outline-accent">
              {#each workspaces as workspace}<option value={workspace.id}>{workspace.name}</option>{/each}
            </select>
          </label>
          <label class="block">
            <span class="mb-1 block text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Share through</span>
            <select bind:value={throughSequence} class="h-10 w-full border border-gray-300 bg-white px-2.5 text-xs dark:border-dark-border-subtle dark:bg-dark-surface dark:text-dark-text-secondary focus-visible:outline-2 focus-visible:outline-accent">
              {#each boundaries as boundary}<option value={boundary.sequence}>{boundary.label}</option>{/each}
            </select>
          </label>
        </div>

        <fieldset class="mt-4 border border-gray-200 dark:border-dark-border">
          <legend class="ml-3 px-1 text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Include deliberately</legend>
          <div class="divide-y divide-gray-200 dark:divide-dark-border">
            <label class="flex cursor-pointer gap-3 px-3 py-2.5"><input type="checkbox" bind:checked={options.include_system_prompt} class="mt-0.5 h-4 w-4 accent-purple-600" /><span><span class="block text-xs font-medium text-gray-800 dark:text-dark-text">System prompt</span><span class="mt-0.5 block text-[11px] text-gray-500 dark:text-dark-text-muted">May contain instructions you normally keep private.</span></span></label>
            <label class="flex cursor-pointer gap-3 px-3 py-2.5"><input type="checkbox" bind:checked={options.include_tool_outputs} class="mt-0.5 h-4 w-4 accent-purple-600" /><span><span class="block text-xs font-medium text-gray-800 dark:text-dark-text">Tool calls and outputs</span><span class="mt-0.5 block text-[11px] text-gray-500 dark:text-dark-text-muted">Copied as historical text only; tools never run on import.</span></span></label>
            <label class="flex cursor-pointer gap-3 px-3 py-2.5"><input type="checkbox" bind:checked={options.include_attachments} class="mt-0.5 h-4 w-4 accent-purple-600" /><span><span class="block text-xs font-medium text-gray-800 dark:text-dark-text">Image attachments</span><span class="mt-0.5 block text-[11px] text-gray-500 dark:text-dark-text-muted">Creates separate share-owned copies; no other private media becomes visible.</span></span></label>
          </div>
        </fieldset>

        {#if preview}
          <div class="mt-4 border border-purple-200 bg-purple-50 px-3 py-2.5 dark:border-purple-900/60 dark:bg-purple-900/10">
            <div class="flex items-center gap-1.5 text-xs font-medium text-purple-900 dark:text-purple-200"><Check size={13} /> Snapshot ready</div>
            <p class="mt-1 text-[11px] text-purple-800 dark:text-purple-300">{preview.payload.messages.length} messages · {preview.attachment_count} attachment{preview.attachment_count === 1 ? '' : 's'} · no workbench configuration or credentials</p>
          </div>
        {/if}
        {#if share}
          <div class="mt-4 border border-gray-200 px-3 py-2.5 dark:border-dark-border">
            <div class="text-xs font-medium text-gray-800 dark:text-dark-text">Published version {share.version}</div>
            <p class="mt-1 text-[11px] text-gray-500 dark:text-dark-text-muted">Recipients see the saved snapshot through message {share.through_sequence}. Existing imported copies remain independent.</p>
          </div>
        {/if}
      {/if}
    </div>

    <footer class="flex flex-wrap items-center justify-between gap-2 border-t border-gray-200 bg-gray-50 px-4 py-3 dark:border-dark-border dark:bg-dark-base">
      <div class="flex gap-2">
        {#if share}
          <button onclick={copyLink} class="h-9 inline-flex items-center gap-1.5 border border-gray-300 bg-white px-3 text-xs text-gray-700 hover:bg-gray-100 focus-visible:outline-2 focus-visible:outline-accent dark:border-dark-border-subtle dark:bg-dark-surface dark:text-dark-text-secondary dark:hover:bg-dark-elevated"><Clipboard size={13} /> Copy link</button>
          <button onclick={() => confirmRevoke ? revoke() : (confirmRevoke = true)} disabled={working} class="h-9 inline-flex items-center gap-1.5 border border-red-300 bg-white px-3 text-xs text-red-700 hover:bg-red-50 disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent dark:border-red-900/60 dark:bg-dark-surface dark:text-red-400 dark:hover:bg-red-900/20"><Trash2 size={13} /> {confirmRevoke ? 'Confirm revoke' : 'Revoke'}</button>
        {/if}
      </div>
      <div class="ml-auto flex gap-2">
        <button onclick={buildPreview} disabled={working || !throughSequence} class="h-9 inline-flex items-center gap-1.5 border border-gray-300 bg-white px-3 text-xs text-gray-700 hover:bg-gray-100 disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent dark:border-dark-border-subtle dark:bg-dark-surface dark:text-dark-text-secondary dark:hover:bg-dark-elevated"><Eye size={13} /> Preview</button>
        <button onclick={saveShare} disabled={working || !throughSequence} class="h-9 inline-flex items-center gap-1.5 bg-purple-600 px-3 text-xs font-medium text-white hover:bg-purple-700 disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-accent">
          {#if working}<Loader2 size={13} class="animate-spin" />{:else if share}<RefreshCw size={13} />{:else}<Share2 size={13} />{/if}
          {share ? 'Update snapshot' : 'Publish snapshot'}
        </button>
      </div>
    </footer>
  </div>
</div>
