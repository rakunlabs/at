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
  import { getAgent, type Agent } from '@/lib/api/agents';
  import { formatDateTime } from '@/lib/helper/format';
  import Markdown from '@/lib/components/Markdown.svelte';
  import {
    Brain, Trash2, ArrowLeft, MessageSquare, FileText,
    User, Hash, Clock, Tag, Bot, ChevronDown, ChevronRight,
    Settings,
  } from 'lucide-svelte';

  interface Props {
    params?: { id?: string };
  }

  let { params }: Props = $props();

  const memoryId = $derived(params?.id || '');

  storeNavbar.title = 'Memory Detail';

  // ─── State ───

  let memory = $state<AgentMemory | null>(null);
  let agent = $state<Agent | null>(null);
  let messages = $state<AgentMemoryMessages | null>(null);
  let loading = $state(true);
  let loadingMessages = $state(false);
  let activeTab = $state<'summary' | 'conversation'>('summary');
  let deleteConfirm = $state(false);
  let collapsedMessages = $state<Set<number>>(new Set());

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      memory = await getAgentMemory(memoryId);
      // Load agent name
      if (memory?.agent_id) {
        try {
          agent = await getAgent(memory.agent_id);
        } catch {
          // non-fatal
        }
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load memory', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadMessages() {
    if (messages) return;
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
    return JSON.stringify(content, null, 2);
  }

  function hasToolCalls(msg: any): boolean {
    if (!msg.content || !Array.isArray(msg.content)) return false;
    return msg.content.some((b: any) => b.type === 'tool_use' || b.type === 'tool_result');
  }

  function getToolCalls(msg: any): any[] {
    if (!msg.content || !Array.isArray(msg.content)) return [];
    return msg.content.filter((b: any) => b.type === 'tool_use');
  }

  function getToolResults(msg: any): any[] {
    if (!msg.content || !Array.isArray(msg.content)) return [];
    return msg.content.filter((b: any) => b.type === 'tool_result');
  }

  function toggleMessage(idx: number) {
    const next = new Set(collapsedMessages);
    if (next.has(idx)) {
      next.delete(idx);
    } else {
      next.add(idx);
    }
    collapsedMessages = next;
  }

  function getRoleColor(role: string): string {
    switch (role) {
      case 'system': return 'border-l-warning';
      case 'assistant': return 'border-l-primary';
      case 'user': return 'border-l-success';
      case 'tool': return 'border-l-info';
      default: return 'border-l-base-300';
    }
  }

  function getRoleIcon(role: string) {
    switch (role) {
      case 'system': return Settings;
      case 'assistant': return Bot;
      case 'user': return User;
      case 'tool': return Hash;
      default: return MessageSquare;
    }
  }

  $effect(() => {
    if (memoryId) {
      load();
    }
  });
</script>

{#if loading}
  <div class="flex flex-col items-center justify-center p-16 gap-3">
    <span class="loading loading-spinner loading-md text-primary"></span>
    <span class="text-sm text-base-content/50">Loading memory...</span>
  </div>
{:else if memory}
  <div class="p-6 max-w-5xl mx-auto">
    <!-- Back button -->
    <button
      class="btn btn-ghost btn-sm gap-1.5 mb-4 -ml-2 text-base-content/50 hover:text-base-content"
      onclick={() => {
        if (memory?.organization_id) {
          push(`/organizations/${memory.organization_id}/memories`);
        } else {
          history.back();
        }
      }}
    >
      <ArrowLeft class="w-3.5 h-3.5" />
      Back to memories
    </button>

    <!-- Header card -->
    <div class="card bg-base-100 border border-base-200 mb-6">
      <div class="card-body p-5">
        <div class="flex items-start gap-4">
          <!-- Icon -->
          <div class="w-12 h-12 rounded-xl bg-primary/10 flex items-center justify-center shrink-0">
            <Brain class="w-6 h-6 text-primary" />
          </div>

          <!-- Title & meta -->
          <div class="flex-1 min-w-0">
            <h1 class="text-lg font-bold leading-tight mb-2">{memory.summary_l0 || 'Memory Detail'}</h1>

            <!-- Metadata grid -->
            <div class="flex flex-wrap gap-x-5 gap-y-1.5 text-sm text-base-content/60">
              <span class="flex items-center gap-1.5">
                <User class="w-3.5 h-3.5" />
                {agent?.name || memory.agent_id.slice(0, 12)}
              </span>
              {#if memory.task_id}
                <button
                  class="flex items-center gap-1.5 hover:text-primary transition-colors"
                  onclick={() => push(`/tasks/${memory!.task_id}`)}
                >
                  <Hash class="w-3.5 h-3.5" />
                  {memory.task_identifier || memory.task_id.slice(0, 12)}
                </button>
              {/if}
              <span class="flex items-center gap-1.5">
                <Clock class="w-3.5 h-3.5" />
                {formatDateTime(memory.created_at)}
              </span>
            </div>

            <!-- Tags -->
            {#if memory.tags?.length}
              <div class="flex flex-wrap gap-1.5 mt-3">
                {#each memory.tags as tag}
                  <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-base-200 text-xs text-base-content/60">
                    <Tag class="w-2.5 h-2.5" />
                    {tag}
                  </span>
                {/each}
              </div>
            {/if}
          </div>

          <!-- Actions -->
          <div class="shrink-0">
            {#if deleteConfirm}
              <div class="flex gap-1.5">
                <button class="btn btn-error btn-sm" onclick={handleDelete}>Delete</button>
                <button class="btn btn-ghost btn-sm" onclick={() => deleteConfirm = false}>Cancel</button>
              </div>
            {:else}
              <button
                class="btn btn-ghost btn-sm text-base-content/30 hover:text-error"
                onclick={() => deleteConfirm = true}
              >
                <Trash2 class="w-4 h-4" />
              </button>
            {/if}
          </div>
        </div>
      </div>
    </div>

    <!-- Tabs -->
    <div class="flex gap-1 mb-5 border-b border-base-200">
      <button
        class={["px-4 py-2.5 text-sm font-medium transition-colors relative",
          activeTab === 'summary'
            ? "text-primary"
            : "text-base-content/50 hover:text-base-content/80"
        ]}
        onclick={() => switchTab('summary')}
      >
        <span class="flex items-center gap-1.5">
          <FileText class="w-4 h-4" />
          Summary
        </span>
        {#if activeTab === 'summary'}
          <div class="absolute bottom-0 left-0 right-0 h-0.5 bg-primary rounded-full"></div>
        {/if}
      </button>
      <button
        class={["px-4 py-2.5 text-sm font-medium transition-colors relative",
          activeTab === 'conversation'
            ? "text-primary"
            : "text-base-content/50 hover:text-base-content/80"
        ]}
        onclick={() => switchTab('conversation')}
      >
        <span class="flex items-center gap-1.5">
          <MessageSquare class="w-4 h-4" />
          Conversation
          {#if messages?.messages}
            <span class="text-xs px-1.5 py-0.5 rounded-full bg-base-200 text-base-content/50">{messages.messages.length}</span>
          {/if}
        </span>
        {#if activeTab === 'conversation'}
          <div class="absolute bottom-0 left-0 right-0 h-0.5 bg-primary rounded-full"></div>
        {/if}
      </button>
    </div>

    <!-- Tab Content -->
    {#if activeTab === 'summary'}
      <div class="card bg-base-100 border border-base-200">
        <div class="card-body p-6">
          {#if memory.summary_l1}
            <Markdown source={memory.summary_l1} class="max-w-none" enhance />
          {:else}
            <p class="text-base-content/40 text-center py-8">No detailed summary available.</p>
          {/if}
        </div>
      </div>
    {:else}
      {#if loadingMessages}
        <div class="flex flex-col items-center justify-center py-12 gap-3">
          <span class="loading loading-spinner loading-md text-primary"></span>
          <span class="text-sm text-base-content/50">Loading conversation...</span>
        </div>
      {:else if messages?.messages?.length}
        <div class="space-y-2">
          {#each messages.messages as msg, idx}
            {@const text = getMessageText(msg.content)}
            {@const toolCalls = getToolCalls(msg)}
            {@const toolResults = getToolResults(msg)}
            {@const isLong = text.length > 800}
            {@const isCollapsed = isLong && !collapsedMessages.has(idx)}
            {@const RoleIcon = getRoleIcon(msg.role)}

            <div class={["card bg-base-100 border border-base-200 border-l-4 overflow-hidden", getRoleColor(msg.role)]}>
              <!-- Message header -->
              <button
                class="flex items-center gap-2 px-4 py-2.5 w-full text-left hover:bg-base-50 transition-colors"
                onclick={() => toggleMessage(idx)}
              >
                <RoleIcon class="w-3.5 h-3.5 text-base-content/40 shrink-0" />
                <span class="text-xs font-semibold uppercase tracking-wide text-base-content/50">{msg.role}</span>
                {#if toolCalls.length}
                  <span class="text-xs text-info/60 font-mono">{toolCalls.length} tool call{toolCalls.length > 1 ? 's' : ''}</span>
                {/if}
                {#if toolResults.length}
                  <span class="text-xs text-info/60 font-mono">tool result</span>
                {/if}
                {#if isLong}
                  <span class="text-xs text-base-content/30 ml-auto">
                    {text.length} chars
                  </span>
                {/if}
                {#if collapsedMessages.has(idx)}
                  <ChevronDown class="w-3.5 h-3.5 text-base-content/30 ml-auto" />
                {:else}
                  <ChevronRight class="w-3.5 h-3.5 text-base-content/30 ml-auto" />
                {/if}
              </button>

              <!-- Message body -->
              {#if collapsedMessages.has(idx) || !isLong}
                <div class="px-4 pb-3 border-t border-base-200">
                  {#if text}
                    <Markdown source={text} class="max-w-none mt-3" />
                  {/if}

                  {#if toolCalls.length}
                    <div class="mt-3 space-y-2">
                      {#each toolCalls as tc}
                        <div class="rounded-lg bg-base-200/50 p-3">
                          <div class="flex items-center gap-1.5 mb-1.5">
                            <Hash class="w-3 h-3 text-info" />
                            <span class="text-xs font-mono font-semibold text-info">{tc.name}</span>
                          </div>
                          {#if tc.input || tc.arguments}
                            <pre class="text-xs bg-base-300/50 rounded p-2 overflow-x-auto max-h-40 overflow-y-auto">{JSON.stringify(tc.input || tc.arguments, null, 2)}</pre>
                          {/if}
                        </div>
                      {/each}
                    </div>
                  {/if}

                  {#if toolResults.length}
                    <div class="mt-3 space-y-2">
                      {#each toolResults as tr}
                        <div class="rounded-lg bg-base-200/50 p-3">
                          <span class="text-xs font-mono text-base-content/50">Result</span>
                          <pre class="text-xs mt-1 bg-base-300/50 rounded p-2 overflow-x-auto max-h-40 overflow-y-auto">{typeof tr.content === 'string' ? tr.content : JSON.stringify(tr.content, null, 2)}</pre>
                        </div>
                      {/each}
                    </div>
                  {/if}
                </div>
              {:else}
                <!-- Preview for long messages -->
                <div class="px-4 pb-3 border-t border-base-200">
                  <p class="text-sm text-base-content/50 mt-2 line-clamp-2">{text.slice(0, 200)}...</p>
                  <button
                    class="text-xs text-primary mt-1 hover:underline"
                    onclick={() => toggleMessage(idx)}
                  >
                    Show full message
                  </button>
                </div>
              {/if}
            </div>
          {/each}
        </div>
      {:else}
        <div class="flex flex-col items-center justify-center py-12 gap-3">
          <MessageSquare class="w-8 h-8 text-base-content/20" />
          <p class="text-base-content/40">No conversation messages available.</p>
        </div>
      {/if}
    {/if}
  </div>
{:else}
  <div class="flex flex-col items-center justify-center p-16 gap-4">
    <Brain class="w-12 h-12 text-base-content/15" />
    <p class="text-base-content/40">Memory not found.</p>
    <button class="btn btn-ghost btn-sm" onclick={() => history.back()}>Go back</button>
  </div>
{/if}

<style>
  @reference "tailwindcss";

  .line-clamp-2 {
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
</style>
