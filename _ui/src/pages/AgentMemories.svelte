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
  import DataTable from '@/lib/components/DataTable.svelte';
  import { Brain, Search, Trash2, RefreshCw, ExternalLink } from 'lucide-svelte';

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
      // Load agent details for names
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

  $effect(() => {
    if (orgId) {
      load();
      loadAgents();
    }
  });
</script>

<div class="p-6 max-w-7xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-6">
    <div class="flex items-center gap-3">
      <Brain class="w-6 h-6 text-primary" />
      <h1 class="text-2xl font-bold">Agent Memories</h1>
    </div>
    <button class="btn btn-sm btn-ghost" onclick={() => load()} disabled={loading}>
      <RefreshCw class={"w-4 h-4" + (loading ? " animate-spin" : "")} />
      Refresh
    </button>
  </div>

  <!-- Filters -->
  <div class="flex gap-4 mb-4">
    <div class="flex-1">
      <div class="join w-full">
        <input
          type="text"
          class="input input-bordered join-item w-full"
          placeholder="Search memories..."
          bind:value={searchQuery}
          onkeydown={(e: KeyboardEvent) => e.key === 'Enter' && handleSearch()}
        />
        <button class="btn btn-bordered join-item" onclick={handleSearch}>
          <Search class="w-4 h-4" />
        </button>
      </div>
    </div>
    <select
      class="select select-bordered w-64"
      bind:value={filterAgentId}
      onchange={() => load()}
    >
      <option value="">All agents</option>
      {#each orgAgents as oa}
        <option value={oa.agent_id}>{getAgentName(oa.agent_id)}</option>
      {/each}
    </select>
  </div>

  <!-- Table -->
  <DataTable items={memories} {loading} emptyTitle="No memories found" emptyDescription="Memories will appear here after agents complete tasks.">
    {#snippet header()}
      <th>Agent</th>
      <th>Task</th>
      <th>Summary</th>
      <th>Tags</th>
      <th>Created</th>
      <th class="w-20"></th>
    {/snippet}
    {#snippet row(mem: AgentMemory)}
      <td class="font-medium">{getAgentName(mem.agent_id)}</td>
      <td>
        {#if mem.task_id}
          <button class="link link-primary text-sm" onclick={() => push(`/tasks/${mem.task_id}`)}>
            {mem.task_identifier || mem.task_id.slice(0, 8)}
          </button>
        {:else}
          <span class="text-base-content/50">-</span>
        {/if}
      </td>
      <td class="max-w-md truncate">
        <button
          class="text-left hover:text-primary cursor-pointer"
          onclick={() => push(`/agent-memories/${mem.id}`)}
        >
          {mem.summary_l0 || '(no summary)'}
        </button>
      </td>
      <td>
        <div class="flex flex-wrap gap-1">
          {#each (mem.tags || []).slice(0, 3) as tag}
            <span class="badge badge-sm badge-outline">{tag}</span>
          {/each}
          {#if (mem.tags || []).length > 3}
            <span class="badge badge-sm badge-ghost">+{mem.tags.length - 3}</span>
          {/if}
        </div>
      </td>
      <td class="text-sm text-base-content/60">{formatDate(mem.created_at)}</td>
      <td>
        <div class="flex gap-1">
          <button class="btn btn-ghost btn-xs" onclick={() => push(`/agent-memories/${mem.id}`)}>
            <ExternalLink class="w-3 h-3" />
          </button>
          {#if deleteConfirm === mem.id}
            <button class="btn btn-error btn-xs" onclick={() => handleDelete(mem.id)}>Confirm</button>
            <button class="btn btn-ghost btn-xs" onclick={() => deleteConfirm = null}>Cancel</button>
          {:else}
            <button class="btn btn-ghost btn-xs text-error" onclick={() => deleteConfirm = mem.id}>
              <Trash2 class="w-3 h-3" />
            </button>
          {/if}
        </div>
      </td>
    {/snippet}
  </DataTable>
</div>
