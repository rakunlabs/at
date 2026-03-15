<script lang="ts">
  import { push } from 'svelte-spa-router';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    getAgentMemory,
    getAgentMemoryMessages,
    deleteAgentMemory,
    type AgentMemory,
    type AgentMemoryMessages,
  } from '@/lib/api/agent-memory';
  import { formatDate } from '@/lib/helper/format';
  import { Brain, Trash2, ArrowLeft, MessageSquare, FileText } from 'lucide-svelte';

  interface Props {
    params?: { id?: string };
  }

  let { params }: Props = $props();

  const memoryId = $derived(params?.id || '');

  storeNavbar.title = 'Memory Detail';

  // ─── State ───

  let memory = $state<AgentMemory | null>(null);
  let messages = $state<AgentMemoryMessages | null>(null);
  let loading = $state(true);
  let loadingMessages = $state(false);
  let activeTab = $state<'summary' | 'conversation'>('summary');
  let deleteConfirm = $state(false);

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      memory = await getAgentMemory(memoryId);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load memory', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadMessages() {
    if (messages) return; // already loaded
    loadingMessages = true;
    try {
      messages = await getAgentMemoryMessages(memoryId);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load messages', 'alert');
    } finally {
      loadingMessages = false;
    }
  }

  async function handleDelete() {
    try {
      await deleteAgentMemory(memoryId);
      addToast('Memory deleted');
      // Navigate back to org memories
      if (memory?.organization_id) {
        push(`/organizations/${memory.organization_id}/memories`);
      } else {
        history.back();
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete memory', 'alert');
    }
  }

  function switchTab(tab: 'summary' | 'conversation') {
    activeTab = tab;
    if (tab === 'conversation') {
      loadMessages();
    }
  }

  function getMessageText(content: any): string {
    if (typeof content === 'string') return content;
    if (Array.isArray(content)) {
      return content
        .filter((b: any) => b.type === 'text' && b.text)
        .map((b: any) => b.text)
        .join('\n');
    }
    return JSON.stringify(content);
  }

  $effect(() => {
    if (memoryId) {
      load();
    }
  });
</script>

{#if loading}
  <div class="flex items-center justify-center p-12">
    <span class="loading loading-spinner loading-lg"></span>
  </div>
{:else if memory}
  <div class="p-6 max-w-4xl mx-auto">
    <!-- Header -->
    <div class="flex items-center justify-between mb-6">
      <div class="flex items-center gap-3">
        <button class="btn btn-ghost btn-sm" onclick={() => history.back()}>
          <ArrowLeft class="w-4 h-4" />
        </button>
        <Brain class="w-6 h-6 text-primary" />
        <div>
          <h1 class="text-xl font-bold">{memory.summary_l0 || 'Memory Detail'}</h1>
          <p class="text-sm text-base-content/60">
            {memory.task_identifier || memory.task_id} &middot; {formatDate(memory.created_at)}
          </p>
        </div>
      </div>
      <div class="flex gap-2">
        {#if deleteConfirm}
          <button class="btn btn-error btn-sm" onclick={handleDelete}>Confirm Delete</button>
          <button class="btn btn-ghost btn-sm" onclick={() => deleteConfirm = false}>Cancel</button>
        {:else}
          <button class="btn btn-ghost btn-sm text-error" onclick={() => deleteConfirm = true}>
            <Trash2 class="w-4 h-4" />
            Delete
          </button>
        {/if}
      </div>
    </div>

    <!-- Metadata -->
    <div class="flex flex-wrap gap-4 mb-6">
      <div class="badge badge-lg badge-outline">Agent: {memory?.agent_id?.slice(0, 8)}</div>
      {#if memory?.task_id}
        <button class="badge badge-lg badge-primary cursor-pointer" onclick={() => push(`/tasks/${memory?.task_id}`)}>
          Task: {memory?.task_identifier || memory?.task_id?.slice(0, 8)}
        </button>
      {/if}
      {#each (memory?.tags || []) as tag}
        <span class="badge badge-lg">{tag}</span>
      {/each}
    </div>

    <!-- Tabs -->
    <div role="tablist" class="tabs tabs-bordered mb-6">
      <button
        role="tab"
        class={["tab", activeTab === 'summary' ? "tab-active" : ""]}
        onclick={() => switchTab('summary')}
      >
        <FileText class="w-4 h-4 mr-2" />
        Summary
      </button>
      <button
        role="tab"
        class={["tab", activeTab === 'conversation' ? "tab-active" : ""]}
        onclick={() => switchTab('conversation')}
      >
        <MessageSquare class="w-4 h-4 mr-2" />
        Full Conversation
      </button>
    </div>

    <!-- Tab Content -->
    {#if activeTab === 'summary'}
      <div class="prose max-w-none dark:prose-invert">
        {#if memory.summary_l1}
          {@html memory.summary_l1.replace(/\n/g, '<br>')}
        {:else}
          <p class="text-base-content/50">No detailed summary available.</p>
        {/if}
      </div>
    {:else}
      {#if loadingMessages}
        <div class="flex items-center justify-center p-8">
          <span class="loading loading-spinner"></span>
          <span class="ml-2">Loading conversation...</span>
        </div>
      {:else if messages && messages.messages}
        <div class="space-y-4">
          {#each messages.messages as msg}
            <div class={["chat", msg.role === 'assistant' ? 'chat-start' : 'chat-end']}>
              <div class="chat-header text-xs opacity-60">{msg.role}</div>
              <div class={["chat-bubble", msg.role === 'system' ? 'chat-bubble-accent' : msg.role === 'assistant' ? 'chat-bubble-primary' : '']}>
                <pre class="whitespace-pre-wrap text-sm">{getMessageText(msg.content)}</pre>
              </div>
            </div>
          {/each}
        </div>
      {:else}
        <p class="text-base-content/50 text-center py-8">No conversation messages available.</p>
      {/if}
    {/if}
  </div>
{:else}
  <div class="flex flex-col items-center justify-center p-12">
    <p class="text-base-content/50">Memory not found.</p>
    <button class="btn btn-ghost btn-sm mt-4" onclick={() => history.back()}>Go back</button>
  </div>
{/if}
