<script lang="ts">
  import { push, querystring } from 'svelte-spa-router';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listOrgMemories,
    searchOrgMemories,
    deleteAgentMemory,
    type AgentMemory,
  } from '@/lib/api/agent-memory';
  import { listOrgAgents, type OrganizationAgent } from '@/lib/api/organizations';
  import { listAgents, type Agent } from '@/lib/api/agents';
  import { formatDate } from '@/lib/helper/format';
  import { Brain, Search, Trash2, RefreshCw, Clock, Tag, User, ChevronRight, Hash } from 'lucide-svelte';

  interface Props {
    params?: { id?: string };
  }

  let { params }: Props = $props();

  const orgId = $derived(params?.id || '');

  storeNavbar.title = 'Agent Memories';

  // ─── State ───

  let memories = $state<AgentMemory[]>([]);
  let loading = $state(true);
  let orgAgents = $state<OrganizationAgent[]>([]);
  let agentMap = $state<Record<string, Agent>>({});

  // Filters
  let searchQuery = $state('');
  let filterAgentId = $state('');
  let deleteConfirm = $state<string | null>(null);

  // Parse initial agent_id from query string
  const qs = new URLSearchParams($querystring || '');
  if (qs.get('agent_id')) {
    filterAgentId = qs.get('agent_id')!;
  }

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      if (searchQuery) {
        memories = await searchOrgMemories(orgId, searchQuery, filterAgentId || undefined);
      } else {
        memories = await listOrgMemories(orgId, filterAgentId || undefined);
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load memories', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadAgents() {
    try {
      orgAgents = await listOrgAgents(orgId);
      const allAgents = await listAgents({ _limit: 100 });
      const map: Record<string, Agent> = {};
      for (const a of allAgents.data) {
        map[a.id] = a;
      }
      agentMap = map;
    } catch {
      // non-fatal
    }
  }

  function handleSearch() {
    load();
  }

  async function handleDelete(id: string) {
    try {
      await deleteAgentMemory(id);
      addToast('Memory deleted');
      deleteConfirm = null;
      load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete memory', 'alert');
    }
  }

  function getAgentName(agentId: string): string {
    return agentMap[agentId]?.name || agentId.slice(0, 8);
  }

  function relativeTime(dateStr: string): string {
    const now = Date.now();
    const then = new Date(dateStr).getTime();
    const diff = now - then;
    const mins = Math.floor(diff / 60000);
    if (mins < 1) return 'just now';
    if (mins < 60) return `${mins}m ago`;
    const hours = Math.floor(mins / 60);
    if (hours < 24) return `${hours}h ago`;
    const days = Math.floor(hours / 24);
    if (days < 7) return `${days}d ago`;
    return formatDate(dateStr);
  }

  $effect(() => {
    if (orgId) {
      load();
      loadAgents();
    }
  });
</script>

<div class="p-6 max-w-6xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-6">
    <div class="flex items-center gap-3">
      <div class="w-10 h-10 rounded-xl bg-primary/10 flex items-center justify-center">
        <Brain class="w-5 h-5 text-primary" />
      </div>
      <div>
        <h1 class="text-xl font-bold">Agent Memories</h1>
        <p class="text-sm text-base-content/50">
          {#if loading}
            Loading...
          {:else}
            {memories.length} {memories.length === 1 ? 'memory' : 'memories'}
            {#if filterAgentId}
              for {getAgentName(filterAgentId)}
            {/if}
          {/if}
        </p>
      </div>
    </div>
    <button class="btn btn-sm btn-ghost gap-1.5" onclick={() => load()} disabled={loading}>
      <RefreshCw class={"w-3.5 h-3.5" + (loading ? " animate-spin" : "")} />
      Refresh
    </button>
  </div>

  <!-- Filters -->
  <div class="flex gap-3 mb-6">
    <div class="flex-1 relative">
      <Search class="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2 text-base-content/40" />
      <input
        type="text"
        class="input input-bordered w-full pl-10 input-sm"
        placeholder="Search memories by content, tags, or keywords..."
        bind:value={searchQuery}
        onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && handleSearch()}
      />
    </div>
    <select
      class="select select-bordered select-sm w-56"
      bind:value={filterAgentId}
      onchange={() => load()}
    >
      <option value="">All agents</option>
      {#each orgAgents as oa}
        <option value={oa.agent_id}>{getAgentName(oa.agent_id)}</option>
      {/each}
    </select>
  </div>

  <!-- Content -->
  {#if loading}
    <div class="flex flex-col items-center justify-center py-16 gap-3">
      <span class="loading loading-spinner loading-md text-primary"></span>
      <span class="text-sm text-base-content/50">Loading memories...</span>
    </div>
  {:else if memories.length === 0}
    <div class="flex flex-col items-center justify-center py-20 gap-4">
      <div class="w-16 h-16 rounded-2xl bg-base-200 flex items-center justify-center">
        <Brain class="w-8 h-8 text-base-content/20" />
      </div>
      <div class="text-center">
        <p class="font-medium text-base-content/60">No memories found</p>
        <p class="text-sm text-base-content/40 mt-1">
          {#if searchQuery}
            Try a different search query.
          {:else}
            Memories will appear here after agents complete tasks.
          {/if}
        </p>
      </div>
    </div>
  {:else}
    <div class="space-y-3">
      {#each memories as mem (mem.id)}
        <div
          role="button"
          tabindex="0"
          class="w-full text-left card bg-base-100 border border-base-200 hover:border-primary/30 hover:shadow-sm transition-all duration-150 cursor-pointer"
          onclick={() => push(`/agent-memories/${mem.id}`)}
          onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); push(`/agent-memories/${mem.id}`); } }}
        >
          <div class="card-body p-4">
            <div class="flex items-start gap-3">
              <!-- Agent avatar -->
              <div class="w-8 h-8 rounded-lg bg-primary/10 flex items-center justify-center shrink-0 mt-0.5">
                <User class="w-4 h-4 text-primary" />
              </div>

              <!-- Main content -->
              <div class="flex-1 min-w-0">
                <!-- Header row -->
                <div class="flex items-center gap-2 mb-1">
                  <span class="font-semibold text-sm">{getAgentName(mem.agent_id)}</span>
                  {#if mem.task_identifier || mem.task_id}
                    <span class="text-xs text-base-content/40 font-mono flex items-center gap-0.5">
                      <Hash class="w-3 h-3" />
                      {mem.task_identifier || mem.task_id.slice(0, 8)}
                    </span>
                  {/if}
                  <span class="text-xs text-base-content/40 flex items-center gap-0.5 ml-auto shrink-0">
                    <Clock class="w-3 h-3" />
                    {relativeTime(mem.created_at)}
                  </span>
                </div>

                <!-- Summary -->
                <p class="text-sm text-base-content/80 line-clamp-2 mb-2">
                  {mem.summary_l0 || '(no summary)'}
                </p>

                <!-- Tags & actions -->
                <div class="flex items-center gap-2 flex-wrap">
                  {#each (mem.tags || []).slice(0, 5) as tag}
                    <span class="inline-flex items-center gap-1 px-2 py-0.5 rounded-full bg-base-200 text-xs text-base-content/60">
                      <Tag class="w-2.5 h-2.5" />
                      {tag}
                    </span>
                  {/each}
                  {#if (mem.tags || []).length > 5}
                    <span class="text-xs text-base-content/40">+{mem.tags.length - 5} more</span>
                  {/if}

                  <div class="ml-auto flex items-center gap-1">
                    {#if deleteConfirm === mem.id}
                      <button
                        class="btn btn-error btn-xs"
                        onclick={(e) => { e.stopPropagation(); handleDelete(mem.id); }}
                      >
                        Delete
                      </button>
                      <button
                        class="btn btn-ghost btn-xs"
                        onclick={(e) => { e.stopPropagation(); deleteConfirm = null; }}
                      >
                        Cancel
                      </button>
                    {:else}
                      <button
                        class="btn btn-ghost btn-xs text-base-content/30 hover:text-error"
                        onclick={(e) => { e.stopPropagation(); deleteConfirm = mem.id; }}
                      >
                        <Trash2 class="w-3 h-3" />
                      </button>
                    {/if}
                    <ChevronRight class="w-4 h-4 text-base-content/20" />
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  @reference "tailwindcss";

  .line-clamp-2 {
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
</style>
