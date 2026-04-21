<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { push } from 'svelte-spa-router';
  import {
    listAuditEntries,
    type AuditEntry,
  } from '@/lib/api/audit';
  import { listAgents, type Agent } from '@/lib/api/agents';
  import { listOrganizations, type Organization } from '@/lib/api/organizations';
  import { listTasks, type Task } from '@/lib/api/tasks';
  import { formatDateTime } from '@/lib/helper/format';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import {
    ScrollText, RefreshCw, ChevronRight, Copy, ExternalLink,
    Bot, User, Cpu, Building2, ClipboardList, Wrench, Workflow,
  } from 'lucide-svelte';

  storeNavbar.title = 'Audit Trail';

  // ─── Reference Data (for resolving IDs → names) ───

  let agents = $state<Agent[]>([]);
  let organizations = $state<Organization[]>([]);
  let tasks = $state<Task[]>([]);

  async function loadReferenceData() {
    try {
      const [agentsRes, orgsRes, tasksRes] = await Promise.all([
        listAgents({ _limit: 500 }),
        listOrganizations({ _limit: 200 }),
        listTasks({ _limit: 500 }),
      ]);
      agents = agentsRes.data || [];
      organizations = orgsRes.data || [];
      tasks = tasksRes.data || [];
    } catch {
      // Non-fatal; unresolved IDs just show as raw
    }
  }

  function agentName(id: string): string {
    if (!id) return '';
    const a = agents.find(x => x.id === id);
    return a?.name || '';
  }

  function orgName(id: string): string {
    if (!id) return '';
    const o = organizations.find(x => x.id === id);
    return o?.name || '';
  }

  function taskLabel(id: string): string {
    if (!id) return '';
    const t = tasks.find(x => x.id === id);
    if (!t) return '';
    return t.identifier ? `${t.identifier} · ${t.title}` : t.title;
  }

  /**
   * Resolve a resource ID to a human-readable label based on its type.
   * Falls back to truncated ID.
   */
  function resolveResource(type: string, id: string): string {
    if (!id) return '';
    switch (type) {
      case 'agent': return agentName(id) || shortId(id);
      case 'task': return taskLabel(id) || shortId(id);
      case 'organization': return orgName(id) || shortId(id);
      case 'tool':
        // tool_call IDs are LLM-generated (e.g. "toolu_...") — just truncate
        return shortId(id);
      default:
        return shortId(id);
    }
  }

  function resolveActor(type: string, id: string): string {
    if (!id) return '';
    if (type === 'agent') return agentName(id) || shortId(id);
    if (type === 'system') return 'system';
    if (type === 'user') return id; // user IDs are usually usernames
    return shortId(id);
  }

  function shortId(id: string): string {
    if (!id) return '';
    return id.length > 14 ? id.slice(0, 8) + '…' + id.slice(-4) : id;
  }

  // ─── State ───

  let entries = $state<AuditEntry[]>([]);
  let loading = $state(true);
  let expandedIds = $state<Set<string>>(new Set());

  // Pagination
  let offset = $state(0);
  let limit = $state(25);
  let total = $state(0);

  // Search & Sort & Filters
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);
  let filterAction = $state('');
  let filterResourceType = $state('');
  let filterOrgId = $state('');

  // ─── Known actions (observed in the codebase) ───
  // Keep this grouped and label-rich so users understand what they mean.

  const ACTIONS: { value: string; label: string; group: string }[] = [
    // Agent lifecycle / execution
    { value: 'tool_call', label: 'Tool Call', group: 'Agent' },
    { value: 'llm_call', label: 'LLM Call', group: 'Agent' },
    // Task lifecycle
    { value: 'task_started', label: 'Task Started', group: 'Task' },
    { value: 'task_delegated', label: 'Task Delegated', group: 'Task' },
    { value: 'task_checkout', label: 'Task Checkout', group: 'Task' },
    { value: 'task_process_triggered', label: 'Task Process Triggered', group: 'Task' },
    { value: 'status_change', label: 'Status Change', group: 'Task' },
    // Config
    { value: 'config_update', label: 'Config Update', group: 'Config' },
  ];

  const RESOURCE_TYPES = ['agent', 'task', 'tool', 'organization', 'workflow', 'skill'];

  // ─── Load ───

  async function load() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) params['actor_id[like]'] = `%${searchQuery}%`;
      if (filterAction) params['action'] = filterAction;
      if (filterResourceType) params['resource_type'] = filterResourceType;
      if (filterOrgId) params['organization_id'] = filterOrgId;
      const sortParam = buildSortParam(sorts);
      params._sort = sortParam || '-created_at';
      const res = await listAuditEntries(params);
      entries = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load audit entries', 'alert');
    } finally {
      loading = false;
    }
  }

  function handleSearch(value: string) {
    searchQuery = value;
    offset = 0;
    load();
  }

  function handleSort(field: string, multiSort: boolean) {
    sorts = toggleSort(sorts, field, multiSort);
    offset = 0;
    load();
  }

  function handleFilterChange() {
    offset = 0;
    load();
  }

  function clearFilters() {
    filterAction = '';
    filterResourceType = '';
    filterOrgId = '';
    offset = 0;
    load();
  }

  // Load reference data then audit entries
  loadReferenceData();
  load();

  // ─── Helpers ───

  function actionBadgeClass(action: string): string {
    switch (action) {
      case 'tool_call':
        return 'bg-blue-100 dark:bg-blue-900/50 text-blue-700 dark:text-blue-300';
      case 'llm_call':
        return 'bg-indigo-100 dark:bg-indigo-900/50 text-indigo-700 dark:text-indigo-300';
      case 'task_started':
      case 'task_delegated':
        return 'bg-yellow-100 dark:bg-yellow-900/50 text-yellow-700 dark:text-yellow-300';
      case 'task_checkout':
        return 'bg-orange-100 dark:bg-orange-900/50 text-orange-700 dark:text-orange-300';
      case 'task_process_triggered':
        return 'bg-amber-100 dark:bg-amber-900/50 text-amber-700 dark:text-amber-300';
      case 'status_change':
        return 'bg-purple-100 dark:bg-purple-900/50 text-purple-700 dark:text-purple-300';
      case 'config_update':
        return 'bg-green-100 dark:bg-green-900/50 text-green-700 dark:text-green-300';
      default:
        return 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted';
    }
  }

  function actorIcon(type: string) {
    switch (type) {
      case 'agent': return Bot;
      case 'user': return User;
      case 'system': return Cpu;
      default: return User;
    }
  }

  function resourceIcon(type: string) {
    switch (type) {
      case 'agent': return Bot;
      case 'task': return ClipboardList;
      case 'tool': return Wrench;
      case 'organization': return Building2;
      case 'workflow': return Workflow;
      default: return ScrollText;
    }
  }

  function toggleExpand(id: string) {
    const next = new Set(expandedIds);
    if (next.has(id)) {
      next.delete(id);
    } else {
      next.add(id);
    }
    expandedIds = next;
  }

  function expandAll() {
    expandedIds = new Set(entries.map(e => e.id));
  }

  function collapseAll() {
    expandedIds = new Set();
  }

  async function copyText(text: string) {
    try {
      await navigator.clipboard.writeText(text);
      addToast('Copied', 'info');
    } catch {
      addToast('Failed to copy', 'alert');
    }
  }

  function formatDetailValue(value: any): string {
    if (value === null || value === undefined) return 'null';
    if (typeof value === 'object') return JSON.stringify(value, null, 2);
    return String(value);
  }

  /**
   * Pull the most useful 1-line summary out of the details object
   * so the row gives the user real information at a glance.
   */
  function detailsSummary(entry: AuditEntry): string {
    const d = entry.details;
    if (!d || Object.keys(d).length === 0) return '';
    // Prioritize well-known fields based on action.
    if (entry.action === 'tool_call') {
      const bits: string[] = [];
      if (d.tool_name) bits.push(String(d.tool_name));
      if (d.iteration !== undefined) bits.push(`iter ${d.iteration}`);
      if (d.has_error) bits.push('error');
      if (bits.length) return bits.join(' · ');
    }
    if (entry.action === 'llm_call') {
      const bits: string[] = [];
      if (d.model) bits.push(String(d.model));
      if (d.provider) bits.push(String(d.provider));
      if (d.input_tokens !== undefined || d.output_tokens !== undefined) {
        bits.push(`${d.input_tokens ?? 0}↑ ${d.output_tokens ?? 0}↓`);
      }
      if (bits.length) return bits.join(' · ');
    }
    if (entry.action === 'status_change') {
      if (d.from && d.to) return `${d.from} → ${d.to}`;
    }
    if (entry.action === 'task_delegated' || entry.action === 'task_started') {
      if (d.task_title) return String(d.task_title);
      if (d.target_agent) return `→ ${String(d.target_agent)}`;
    }
    // Generic fallback — show first 2 keys.
    const keys = Object.keys(d).slice(0, 2);
    return keys.map(k => `${k}: ${formatDetailValue(d[k]).slice(0, 40)}`).join(' · ');
  }

  function relativeTime(iso: string): string {
    if (!iso) return '';
    const t = Date.parse(iso);
    if (Number.isNaN(t)) return '';
    const diff = Date.now() - t;
    if (diff < 0) return '';
    if (diff < 60_000) return 'now';
    if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`;
    if (diff < 86_400_000) return `${Math.floor(diff / 3_600_000)}h ago`;
    if (diff < 30 * 86_400_000) return `${Math.floor(diff / 86_400_000)}d ago`;
    return formatDateTime(iso);
  }

  function navigateToResource(type: string, id: string) {
    if (!id) return;
    switch (type) {
      case 'agent': push('/agents'); break;
      case 'task': push(`/tasks/${id}`); break;
      case 'organization': push(`/organizations/${id}`); break;
      default: break;
    }
  }
</script>

<svelte:head>
  <title>AT | Audit Trail</title>
</svelte:head>

<div class="p-6 max-w-7xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <ScrollText size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Audit Trail</h2>
      <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={expandAll}
        class="px-2 py-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
        title="Expand all"
      >
        Expand all
      </button>
      <button
        onclick={collapseAll}
        class="px-2 py-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
        title="Collapse all"
      >
        Collapse
      </button>
      <button
        onclick={load}
        class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
    </div>
  </div>

  <!-- Filters -->
  <div class="flex flex-wrap items-center gap-2 mb-3 p-3 border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
    <span class="text-xs text-gray-500 dark:text-dark-text-muted font-medium">Filters:</span>

    <select
      bind:value={filterAction}
      onchange={handleFilterChange}
      class="text-xs px-2 py-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated dark:text-dark-text focus:outline-none focus:border-gray-500"
    >
      <option value="">All actions</option>
      {#each ['Agent', 'Task', 'Config'] as group}
        <optgroup label={group}>
          {#each ACTIONS.filter(a => a.group === group) as a}
            <option value={a.value}>{a.label}</option>
          {/each}
        </optgroup>
      {/each}
    </select>

    <select
      bind:value={filterResourceType}
      onchange={handleFilterChange}
      class="text-xs px-2 py-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated dark:text-dark-text focus:outline-none focus:border-gray-500"
    >
      <option value="">All resource types</option>
      {#each RESOURCE_TYPES as t}
        <option value={t}>{t}</option>
      {/each}
    </select>

    <select
      bind:value={filterOrgId}
      onchange={handleFilterChange}
      class="text-xs px-2 py-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated dark:text-dark-text focus:outline-none focus:border-gray-500"
    >
      <option value="">All organizations</option>
      {#each organizations as o}
        <option value={o.id}>{o.name}</option>
      {/each}
    </select>

    {#if filterAction || filterResourceType || filterOrgId}
      <button
        onclick={clearFilters}
        class="text-xs px-2 py-1 text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
      >
        Clear
      </button>
    {/if}
  </div>

  <!-- Audit list -->
  <DataTable
    items={entries}
    {loading}
    {total}
    {limit}
    bind:offset
    onchange={load}
    onsearch={handleSearch}
    searchPlaceholder="Search by actor ID..."
    emptyIcon={ScrollText}
    emptyTitle="No audit entries"
    emptyDescription="Audit entries are recorded automatically when agents perform actions"
  >
    {#snippet header()}
      <SortableHeader field="created_at" label="Time" {sorts} onsort={handleSort} />
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Actor</th>
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Action</th>
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Resource</th>
      <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Summary</th>
    {/snippet}

    {#snippet row(entry)}
      {@const isExpanded = expandedIds.has(entry.id)}
      {@const detailEntries = entry.details ? Object.entries(entry.details) : []}
      {@const ActorIcon = actorIcon(entry.actor_type)}
      {@const ResourceIcon = resourceIcon(entry.resource_type)}
      {@const actorLabel = resolveActor(entry.actor_type, entry.actor_id)}
      {@const resourceLabel = resolveResource(entry.resource_type, entry.resource_id)}
      <tr
        class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors cursor-pointer select-none"
        onclick={() => toggleExpand(entry.id)}
      >
        <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-secondary whitespace-nowrap">
          <div class="flex items-center gap-1.5">
            <ChevronRight
              size={12}
              class="text-gray-400 dark:text-dark-text-secondary transition-transform flex-shrink-0 {isExpanded ? 'rotate-90' : ''}"
            />
            <span title={formatDateTime(entry.created_at)}>{relativeTime(entry.created_at)}</span>
          </div>
        </td>
        <td class="px-4 py-2.5 text-xs text-gray-700 dark:text-dark-text">
          <div class="flex items-center gap-1.5">
            <ActorIcon size={12} class="text-gray-400 dark:text-dark-text-muted flex-shrink-0" />
            <span class="font-medium">{actorLabel || '—'}</span>
            <span class="text-gray-400 dark:text-dark-text-muted text-[10px]">{entry.actor_type}</span>
          </div>
        </td>
        <td class="px-4 py-2.5">
          <span class="inline-block px-2 py-0.5 text-xs font-medium rounded {actionBadgeClass(entry.action)}">
            {entry.action}
          </span>
        </td>
        <td class="px-4 py-2.5 text-xs text-gray-700 dark:text-dark-text">
          {#if entry.resource_id}
            <div class="flex items-center gap-1.5">
              <ResourceIcon size={12} class="text-gray-400 dark:text-dark-text-muted flex-shrink-0" />
              <span>{resourceLabel || '—'}</span>
              <span class="text-gray-400 dark:text-dark-text-muted text-[10px]">{entry.resource_type}</span>
            </div>
          {:else}
            <span class="text-gray-400 dark:text-dark-text-muted">—</span>
          {/if}
        </td>
        <td class="px-4 py-2.5 text-xs text-gray-600 dark:text-dark-text-secondary max-w-md truncate" title={detailsSummary(entry)}>
          {detailsSummary(entry) || '—'}
        </td>
      </tr>
      {#if isExpanded}
        <tr class="bg-gray-50/50 dark:bg-dark-base/30">
          <td colspan="5" class="px-0 py-0">
            <div class="mx-4 my-3 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
              <!-- Metadata header -->
              <div class="grid grid-cols-2 md:grid-cols-4 gap-3 px-4 py-3 border-b border-gray-200 dark:border-dark-border text-xs">
                <div>
                  <div class="text-[10px] uppercase tracking-wider text-gray-400 dark:text-dark-text-muted mb-0.5">Entry ID</div>
                  <div class="flex items-center gap-1">
                    <code class="font-mono text-gray-700 dark:text-dark-text">{shortId(entry.id)}</code>
                    <button class="p-0.5 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" onclick={(e) => { e.stopPropagation(); copyText(entry.id); }} title="Copy ID">
                      <Copy size={10} />
                    </button>
                  </div>
                </div>
                <div>
                  <div class="text-[10px] uppercase tracking-wider text-gray-400 dark:text-dark-text-muted mb-0.5">Timestamp</div>
                  <div class="text-gray-700 dark:text-dark-text">{formatDateTime(entry.created_at)}</div>
                </div>
                <div>
                  <div class="text-[10px] uppercase tracking-wider text-gray-400 dark:text-dark-text-muted mb-0.5">Organization</div>
                  <div class="text-gray-700 dark:text-dark-text">
                    {#if entry.organization_id}
                      {orgName(entry.organization_id) || shortId(entry.organization_id)}
                    {:else}
                      <span class="text-gray-400 dark:text-dark-text-muted">—</span>
                    {/if}
                  </div>
                </div>
                <div>
                  <div class="text-[10px] uppercase tracking-wider text-gray-400 dark:text-dark-text-muted mb-0.5">Action</div>
                  <div class="text-gray-700 dark:text-dark-text">{entry.action}</div>
                </div>

                <div>
                  <div class="text-[10px] uppercase tracking-wider text-gray-400 dark:text-dark-text-muted mb-0.5">Actor</div>
                  <div class="flex items-center gap-1 text-gray-700 dark:text-dark-text">
                    <span class="text-[10px] text-gray-400 dark:text-dark-text-muted">{entry.actor_type}:</span>
                    <span class="font-medium">{actorLabel || '—'}</span>
                    {#if entry.actor_id}
                      <button class="p-0.5 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" onclick={(e) => { e.stopPropagation(); copyText(entry.actor_id); }} title="Copy actor ID">
                        <Copy size={10} />
                      </button>
                    {/if}
                  </div>
                </div>
                <div class="md:col-span-3">
                  <div class="text-[10px] uppercase tracking-wider text-gray-400 dark:text-dark-text-muted mb-0.5">Resource</div>
                  {#if entry.resource_id}
                    <div class="flex items-center gap-1 text-gray-700 dark:text-dark-text">
                      <span class="text-[10px] text-gray-400 dark:text-dark-text-muted">{entry.resource_type}:</span>
                      <span>{resourceLabel || '—'}</span>
                      <code class="font-mono text-[10px] text-gray-400 dark:text-dark-text-muted">{shortId(entry.resource_id)}</code>
                      <button class="p-0.5 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" onclick={(e) => { e.stopPropagation(); copyText(entry.resource_id); }} title="Copy resource ID">
                        <Copy size={10} />
                      </button>
                      {#if entry.resource_type === 'task' || entry.resource_type === 'agent' || entry.resource_type === 'organization'}
                        <button class="p-0.5 text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary" onclick={(e) => { e.stopPropagation(); navigateToResource(entry.resource_type, entry.resource_id); }} title="Open resource">
                          <ExternalLink size={10} />
                        </button>
                      {/if}
                    </div>
                  {:else}
                    <span class="text-gray-400 dark:text-dark-text-muted">—</span>
                  {/if}
                </div>
              </div>

              <!-- Details -->
              <div class="px-4 py-3">
                <div class="flex items-center justify-between mb-2">
                  <div class="text-[10px] uppercase tracking-wider text-gray-400 dark:text-dark-text-muted">Details</div>
                  {#if detailEntries.length > 0}
                    <button
                      class="flex items-center gap-1 text-[10px] text-gray-400 hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
                      onclick={(e) => { e.stopPropagation(); copyText(JSON.stringify(entry.details, null, 2)); }}
                      title="Copy JSON"
                    >
                      <Copy size={10} /> Copy JSON
                    </button>
                  {/if}
                </div>
                {#if detailEntries.length > 0}
                  <div class="divide-y divide-gray-100 dark:divide-dark-border border border-gray-100 dark:border-dark-border">
                    {#each detailEntries as [key, value]}
                      <div class="px-3 py-2 flex gap-4">
                        <span class="text-xs font-medium text-gray-500 dark:text-accent-text min-w-[140px] flex-shrink-0 font-mono">{key}</span>
                        {#if typeof value === 'object' && value !== null}
                          <pre class="text-xs font-mono text-gray-700 dark:text-dark-text whitespace-pre-wrap break-all flex-1">{JSON.stringify(value, null, 2)}</pre>
                        {:else if typeof value === 'boolean'}
                          <span class="inline-block px-1.5 py-0.5 text-xs font-medium rounded {value ? 'bg-green-100 dark:bg-green-900/50 text-green-700 dark:text-green-300' : 'bg-red-100 dark:bg-red-900/50 text-red-700 dark:text-red-300'}">
                            {value}
                          </span>
                        {:else if typeof value === 'number'}
                          <span class="text-xs font-mono text-gray-700 dark:text-dark-text break-all flex-1">{value}</span>
                        {:else}
                          <span class="text-xs font-mono text-gray-700 dark:text-dark-text break-all flex-1">{formatDetailValue(value)}</span>
                        {/if}
                      </div>
                    {/each}
                  </div>
                {:else}
                  <div class="text-xs text-gray-400 dark:text-dark-text-muted italic">No additional details recorded for this entry.</div>
                {/if}
              </div>
            </div>
          </td>
        </tr>
      {/if}
    {/snippet}
  </DataTable>
</div>
