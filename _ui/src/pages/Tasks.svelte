<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { push } from 'svelte-spa-router';
  import {
    listTasks,
    createTask,
    updateTask,
    deleteTask,
    checkoutTask,
    releaseTask,
    processTask,
    TASK_STATUSES,
    TASK_STATUS_LABELS,
    TASK_PRIORITIES,
    TASK_PRIORITY_LABELS,
    type Task,
  } from '@/lib/api/tasks';
  import { listOrganizations, type Organization } from '@/lib/api/organizations';
  import { listAgents, type Agent } from '@/lib/api/agents';
  import { formatDate } from '@/lib/helper/format';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import KanbanBoard from '@/lib/components/KanbanBoard.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import {
    ClipboardList, Plus, Pencil, Trash2, X, Save, RefreshCw,
    UserCheck, UserX, List, LayoutGrid, ExternalLink, Building2, Play,
    GitBranch,
  } from 'lucide-svelte';

  storeNavbar.title = 'Tasks';

  // ─── Reference Data ───

  let organizations = $state<Organization[]>([]);
  let agents = $state<Agent[]>([]);

  async function loadReferenceData() {
    try {
      const [orgRes, agentRes] = await Promise.all([
        listOrganizations({ _limit: 200 }),
        listAgents({ _limit: 200 }),
      ]);
      organizations = orgRes.data || [];
      agents = agentRes.data || [];
    } catch {
      // Non-fatal: dropdowns will be empty
    }
  }

  function orgName(id: string): string {
    if (!id) return '';
    const org = organizations.find(o => o.id === id);
    return org?.name || id.substring(0, 12);
  }

  function agentName(id: string): string {
    if (!id) return '';
    const agent = agents.find(a => a.id === id);
    return agent?.name || id.substring(0, 12);
  }

  // ─── State ───

  let tasks = $state<Task[]>([]);
  let allTasks = $state<Task[]>([]);
  let loading = $state(true);

  // Pagination (for list view)
  let offset = $state(0);
  let limit = $state(20);
  let total = $state(0);

  // Search & Sort & Filter
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);
  let filterStatus = $state('');
  let filterPriority = $state('');
  let filterOrgId = $state('');

  // View mode: persisted in localStorage
  let viewMode = $state<'list' | 'board'>(
    (typeof localStorage !== 'undefined' && localStorage.getItem('tasks-view') as 'list' | 'board') || 'list'
  );

  // Sub-task toggle: persisted in localStorage, default OFF (hide sub-tasks)
  let showSubTasks = $state(
    typeof localStorage !== 'undefined' && localStorage.getItem('tasks-show-subtasks') === 'true'
  );

  function toggleSubTasks() {
    showSubTasks = !showSubTasks;
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem('tasks-show-subtasks', String(showSubTasks));
    }
  }

  // Derived filtered tasks (client-side sub-task filtering)
  let filteredTasks = $derived(
    showSubTasks ? tasks : tasks.filter(t => !t.parent_id)
  );
  let filteredAllTasks = $derived(
    showSubTasks ? allTasks : allTasks.filter(t => !t.parent_id)
  );

  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);

  // Context menu state
  let contextMenu = $state<{ x: number; y: number; task: Task } | null>(null);

  function openContextMenu(e: MouseEvent, task: Task) {
    // Only show context menu for tasks that have an organization (can be processed)
    if (!task.organization_id) return;
    e.preventDefault();
    contextMenu = { x: e.clientX, y: e.clientY, task };
  }

  function closeContextMenu() {
    contextMenu = null;
  }

  // Checkout inline state
  let checkoutTaskId = $state<string | null>(null);
  let checkoutAgentId = $state('');

  // Form fields
  let formTitle = $state('');
  let formDescription = $state('');
  let formGoalId = $state('');
  let formAssignedAgentId = $state('');
  let formOrganizationId = $state('');
  let formStatus = $state('todo');
  let formPriorityLevel = $state('');
  let formPriority = $state(0);
  let formProjectId = $state('');
  let formIdentifier = $state('');
  let saving = $state(false);

  // Persist view mode
  function setViewMode(mode: 'list' | 'board') {
    viewMode = mode;
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem('tasks-view', mode);
    }
    if (mode === 'board') {
      loadAll();
    } else {
      load();
    }
  }

  // ─── Status badges ───

  function statusClasses(status: string): string {
    switch (status) {
      case 'backlog':
        return 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted';
      case 'open':
      case 'todo':
        return 'bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-400';
      case 'in_progress':
        return 'bg-yellow-100 dark:bg-yellow-900/30 text-yellow-700 dark:text-yellow-400';
      case 'in_review':
      case 'review':
        return 'bg-purple-100 dark:bg-purple-900/30 text-purple-700 dark:text-purple-400';
      case 'blocked':
        return 'bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-400';
      case 'completed':
      case 'done':
        return 'bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-400';
      case 'cancelled':
        return 'bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted';
      default:
        return 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted';
    }
  }

  // ─── Load ───

  function applyFilters(params: any) {
    if (searchQuery) params['title[like]'] = `%${searchQuery}%`;
    if (filterStatus) params['status'] = filterStatus;
    if (filterPriority) params['priority_level'] = filterPriority;
    if (filterOrgId) params['organization_id'] = filterOrgId;
  }

  async function load() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      applyFilters(params);
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      const res = await listTasks(params);
      tasks = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load tasks', 'alert');
    } finally {
      loading = false;
    }
  }

  // Load all tasks for board view (no pagination)
  async function loadAll() {
    loading = true;
    try {
      const params: any = { _limit: 500 };
      applyFilters(params);
      const res = await listTasks(params);
      allTasks = res.data || [];
      total = res.meta?.total || 0;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load tasks', 'alert');
    } finally {
      loading = false;
    }
  }

  function handleSearch(value: string) {
    searchQuery = value;
    offset = 0;
    if (viewMode === 'board') loadAll();
    else load();
  }

  function handleSort(field: string, multiSort: boolean) {
    sorts = toggleSort(sorts, field, multiSort);
    offset = 0;
    load();
  }

  function handleFilterChange() {
    offset = 0;
    if (viewMode === 'board') loadAll();
    else load();
  }

  // Initial load
  loadReferenceData();
  if (viewMode === 'board') loadAll();
  else load();

  function refresh() {
    if (viewMode === 'board') loadAll();
    else load();
  }

  // ─── Form ───

  function resetForm() {
    formTitle = '';
    formDescription = '';
    formGoalId = '';
    formAssignedAgentId = '';
    formOrganizationId = '';
    formStatus = 'todo';
    formPriorityLevel = '';
    formPriority = 0;
    formProjectId = '';
    formIdentifier = '';
    editingId = null;
    showForm = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(task: Task) {
    resetForm();
    editingId = task.id;
    formTitle = task.title;
    formDescription = task.description || '';
    formGoalId = task.goal_id || '';
    formAssignedAgentId = task.assigned_agent_id || '';
    formOrganizationId = task.organization_id || '';
    formStatus = task.status || 'todo';
    formPriorityLevel = task.priority_level || '';
    formPriority = task.priority || 0;
    formProjectId = task.project_id || '';
    formIdentifier = task.identifier || '';
    showForm = true;
  }

  async function handleSubmit() {
    if (!formTitle.trim()) {
      addToast('Task title is required', 'warn');
      return;
    }

    saving = true;
    try {
      const payload: Partial<Task> = {
        title: formTitle.trim(),
        description: formDescription.trim(),
        goal_id: formGoalId.trim(),
        assigned_agent_id: formAssignedAgentId,
        organization_id: formOrganizationId,
        status: formStatus,
        priority_level: formPriorityLevel,
        priority: formPriority,
        project_id: formProjectId.trim(),
        identifier: formIdentifier.trim(),
      };

      if (editingId) {
        await updateTask(editingId, payload);
        addToast(`Task "${formTitle}" updated`);
      } else {
        await createTask(payload);
        addToast(`Task "${formTitle}" created`);
      }
      resetForm();
      await refresh();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save task', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteTask(id);
      addToast('Task deleted');
      deleteConfirm = null;
      await refresh();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete task', 'alert');
    }
  }

  function openCheckout(task: Task) {
    checkoutTaskId = task.id;
    checkoutAgentId = '';
  }

  async function confirmCheckout() {
    if (!checkoutTaskId || !checkoutAgentId) {
      addToast('Select an agent for checkout', 'warn');
      return;
    }
    try {
      await checkoutTask(checkoutTaskId, checkoutAgentId);
      addToast('Task checked out');
      checkoutTaskId = null;
      checkoutAgentId = '';
      await refresh();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to checkout task', 'alert');
    }
  }

  function cancelCheckout() {
    checkoutTaskId = null;
    checkoutAgentId = '';
  }

  async function handleRelease(task: Task) {
    try {
      await releaseTask(task.id);
      addToast(`Task "${task.title}" released`);
      await refresh();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to release task', 'alert');
    }
  }

  async function handleProcess(task: Task) {
    try {
      await processTask(task.id);
      addToast(`Task "${task.title}" sent for processing`);
      await refresh();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to process task', 'alert');
    }
  }

  function handleBoardStatusChange(taskId: string, newStatus: string) {
    refresh();
  }
</script>

<svelte:head>
  <title>AT | Tasks</title>
</svelte:head>

<div class="p-6 max-w-6xl mx-auto flex flex-col" style="height: calc(100vh - 3rem);">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4 shrink-0">
    <div class="flex items-center gap-2">
      <ClipboardList size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Tasks</h2>
      <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
    </div>
    <div class="flex items-center gap-2">
      <!-- View mode toggle -->
      <div class="flex border border-gray-200 dark:border-dark-border">
        <button
          onclick={() => setViewMode('list')}
          class="p-1.5 transition-colors {viewMode === 'list' ? 'bg-gray-900 text-white dark:bg-accent' : 'text-gray-400 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated'}"
          title="List view"
        >
          <List size={14} />
        </button>
        <button
          onclick={() => setViewMode('board')}
          class="p-1.5 transition-colors {viewMode === 'board' ? 'bg-gray-900 text-white dark:bg-accent' : 'text-gray-400 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated'}"
          title="Board view"
        >
          <LayoutGrid size={14} />
        </button>
      </div>

      <!-- Sub-task toggle -->
      <button
        onclick={toggleSubTasks}
        class="flex items-center gap-1 px-2 py-1.5 text-xs border transition-colors {showSubTasks ? 'border-gray-900 dark:border-accent bg-gray-900 dark:bg-accent text-white' : 'border-gray-200 dark:border-dark-border text-gray-400 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated'}"
        title={showSubTasks ? 'Showing sub-tasks — click to hide' : 'Sub-tasks hidden — click to show'}
      >
        <GitBranch size={12} />
        Sub-tasks
      </button>

      <!-- Filters -->
      <select
        bind:value={filterStatus}
        onchange={handleFilterChange}
        class="border border-gray-200 dark:border-dark-border px-2 py-1 text-xs dark:bg-dark-elevated dark:text-dark-text"
      >
        <option value="">All statuses</option>
        {#each TASK_STATUSES as status}
          <option value={status}>{TASK_STATUS_LABELS[status] || status}</option>
        {/each}
      </select>

      <select
        bind:value={filterPriority}
        onchange={handleFilterChange}
        class="border border-gray-200 dark:border-dark-border px-2 py-1 text-xs dark:bg-dark-elevated dark:text-dark-text"
      >
        <option value="">All priorities</option>
        {#each TASK_PRIORITIES as prio}
          <option value={prio}>{TASK_PRIORITY_LABELS[prio]}</option>
        {/each}
      </select>

      <select
        bind:value={filterOrgId}
        onchange={handleFilterChange}
        class="border border-gray-200 dark:border-dark-border px-2 py-1 text-xs dark:bg-dark-elevated dark:text-dark-text"
      >
        <option value="">All organizations</option>
        {#each organizations as org}
          <option value={org.id}>{org.name}</option>
        {/each}
      </select>

      <button
        onclick={refresh}
        class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
      <button
        onclick={openCreate}
        class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors"
      >
        <Plus size={12} />
        New Task
      </button>
    </div>
  </div>

  <!-- Form -->
  {#if showForm}
    <div class="border border-gray-200 dark:border-dark-border mb-4 bg-white dark:bg-dark-surface overflow-hidden shrink-0">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
          {editingId ? `Edit: ${formTitle}` : 'New Task'}
        </span>
        <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
          <X size={14} />
        </button>
      </div>

      <form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-3">
        <div class="grid grid-cols-2 gap-4">
          <!-- Title -->
          <div>
            <label for="form-title" class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Title</label>
            <input id="form-title" type="text" bind:value={formTitle} placeholder="e.g., Implement user auth"
              class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text transition-colors" />
          </div>
          <!-- Identifier -->
          <div>
            <label for="form-identifier" class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Identifier</label>
            <input id="form-identifier" type="text" bind:value={formIdentifier} placeholder="e.g., PROJ-123"
              class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text transition-colors" />
          </div>
        </div>

        <!-- Description -->
        <div>
          <label for="form-description" class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Description</label>
          <textarea id="form-description" bind:value={formDescription} placeholder="Describe the task (optional)" rows="2"
            class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text transition-colors resize-y"></textarea>
        </div>

        <div class="grid grid-cols-5 gap-3">
          <!-- Status -->
          <div>
            <label for="form-status" class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Status</label>
            <select id="form-status" bind:value={formStatus}
              class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none dark:bg-dark-elevated dark:text-dark-text transition-colors">
              {#each TASK_STATUSES as status}
                <option value={status}>{TASK_STATUS_LABELS[status]}</option>
              {/each}
              <option value="open">Open (legacy)</option>
              <option value="review">Review (legacy)</option>
              <option value="completed">Completed (legacy)</option>
            </select>
          </div>
          <!-- Priority Level -->
          <div>
            <label for="form-priority-level" class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Priority Level</label>
            <select id="form-priority-level" bind:value={formPriorityLevel}
              class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none dark:bg-dark-elevated dark:text-dark-text transition-colors">
              <option value="">None</option>
              {#each TASK_PRIORITIES as prio}
                <option value={prio}>{TASK_PRIORITY_LABELS[prio]}</option>
              {/each}
            </select>
          </div>
          <!-- Organization -->
          <div>
            <label for="form-organization" class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Organization</label>
            <select id="form-organization" bind:value={formOrganizationId}
              class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none dark:bg-dark-elevated dark:text-dark-text transition-colors">
              <option value="">None</option>
              {#each organizations as org}
                <option value={org.id}>{org.name}</option>
              {/each}
            </select>
          </div>
          <!-- Assigned Agent -->
          <div>
            <label for="form-assigned-agent" class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Assigned Agent</label>
            <select id="form-assigned-agent" bind:value={formAssignedAgentId}
              class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none dark:bg-dark-elevated dark:text-dark-text transition-colors">
              <option value="">Unassigned</option>
              {#each agents as agent}
                <option value={agent.id}>{agent.name}</option>
              {/each}
            </select>
          </div>
          <!-- Project ID -->
          <div>
            <label for="form-project-id" class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Project ID</label>
            <input id="form-project-id" type="text" bind:value={formProjectId} placeholder="Project ID"
              class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none dark:bg-dark-elevated dark:text-dark-text transition-colors" />
          </div>
        </div>

        <!-- Actions -->
        <div class="flex justify-end gap-2 pt-3 border-t border-gray-100 dark:border-dark-border">
          <button type="button" onclick={resetForm}
            class="px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary transition-colors">
            Cancel
          </button>
          <button type="submit" disabled={saving}
            class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50">
            <Save size={14} />
            {#if saving}Saving...{:else}{editingId ? 'Update' : 'Create'}{/if}
          </button>
        </div>
      </form>
    </div>
  {/if}

  <!-- Board view -->
  {#if viewMode === 'board'}
    <div class="flex-1 min-h-0">
      {#if loading}
        <div class="flex items-center justify-center h-full">
          <div class="text-sm text-gray-400 dark:text-dark-text-muted">Loading tasks...</div>
        </div>
      {:else}
        <KanbanBoard tasks={filteredAllTasks} {organizations} {agents} onStatusChange={handleBoardStatusChange} onProcess={handleProcess} />
      {/if}
    </div>
  {:else}
    <!-- List view -->
    <div class="flex-1 min-h-0 overflow-auto">
      <DataTable
        items={filteredTasks}
        {loading}
        {total}
        {limit}
        bind:offset
        onchange={load}
        onsearch={handleSearch}
        searchPlaceholder="Search by title..."
        emptyIcon={ClipboardList}
        emptyTitle="No tasks"
        emptyDescription="Tasks are atomic work items that agents can check out and complete"
      >
        {#snippet header()}
          <SortableHeader field="title" label="Title" {sorts} onsort={handleSort} />
          <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Status</th>
          <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Priority</th>
          <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Organization</th>
          <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Assigned Agent</th>
          <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Checked Out</th>
          <SortableHeader field="updated_at" label="Updated" {sorts} onsort={handleSort} />
          <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-36"></th>
        {/snippet}

        {#snippet row(task)}
          <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
          <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors" oncontextmenu={(e) => openContextMenu(e, task)}>
            <td class="px-4 py-2.5">
              <div class="flex items-center gap-2">
                {#if task.identifier}
                  <span class="text-[10px] font-mono text-gray-400 dark:text-dark-text-muted">{task.identifier}</span>
                {/if}
                <a href="#/tasks/{task.id}" class="font-medium text-gray-900 dark:text-dark-text hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
                  {task.title}
                </a>
              </div>
            </td>
            <td class="px-4 py-2.5">
              <span class="inline-block px-2 py-0.5 text-xs font-medium capitalize {statusClasses(task.status)}">
                {TASK_STATUS_LABELS[task.status] || task.status.replace(/_/g, ' ')}
              </span>
            </td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
              {#if task.priority_level}
                <span class="capitalize">{task.priority_level}</span>
              {:else}
                {task.priority || '-'}
              {/if}
            </td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
              {#if task.organization_id}
                <span class="inline-flex items-center gap-1 px-1.5 py-0.5 bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary">
                  <Building2 size={10} />
                  {orgName(task.organization_id)}
                </span>
              {:else}
                -
              {/if}
            </td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
              {task.assigned_agent_id ? agentName(task.assigned_agent_id) : '-'}
            </td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
              {task.checked_out_by ? agentName(task.checked_out_by) : '-'}
            </td>
            <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">{formatDate(task.updated_at)}</td>
            <td class="px-4 py-2.5 text-right">
              <div class="flex justify-end gap-1">
                {#if checkoutTaskId === task.id}
                  <!-- Inline checkout: agent selector + confirm/cancel -->
                  <select
                    bind:value={checkoutAgentId}
                    class="border border-gray-200 dark:border-dark-border px-1.5 py-0.5 text-xs dark:bg-dark-elevated dark:text-dark-text max-w-[120px]"
                  >
                    <option value="">Agent...</option>
                    {#each agents as agent}
                      <option value={agent.id}>{agent.name}</option>
                    {/each}
                  </select>
                  <button
                    onclick={confirmCheckout}
                    disabled={!checkoutAgentId}
                    class="px-2 py-0.5 text-xs bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
                  >
                    OK
                  </button>
                  <button
                    onclick={cancelCheckout}
                    class="px-2 py-0.5 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
                  >
                    Cancel
                  </button>
                {:else}
                  <button
                    onclick={() => push(`/tasks/${task.id}`)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors"
                    title="View details"
                  >
                    <ExternalLink size={14} />
                  </button>
                  <button
                    onclick={() => openCheckout(task)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors"
                    title="Checkout"
                  >
                    <UserCheck size={14} />
                  </button>
                  {#if task.checked_out_by}
                    <button
                      onclick={() => handleRelease(task)}
                      class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors"
                      title="Release"
                    >
                      <UserX size={14} />
                    </button>
                  {/if}
                  <button
                    onclick={() => openEdit(task)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors"
                    title="Edit"
                  >
                    <Pencil size={14} />
                  </button>
                  {#if deleteConfirm === task.id}
                    <button
                      onclick={() => handleDelete(task.id)}
                      class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors"
                    >
                      Confirm
                    </button>
                    <button
                      onclick={() => (deleteConfirm = null)}
                      class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
                    >
                      Cancel
                    </button>
                  {:else}
                    <button
                      onclick={() => (deleteConfirm = task.id)}
                      class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 transition-colors"
                      title="Delete"
                    >
                      <Trash2 size={14} />
                    </button>
                  {/if}
                {/if}
              </div>
            </td>
          </tr>
        {/snippet}
      </DataTable>
    </div>
  {/if}
</div>

<!-- Context menu -->
{#if contextMenu}
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <div
    class="fixed inset-0 z-40"
    onclick={closeContextMenu}
    oncontextmenu={(e) => { e.preventDefault(); closeContextMenu(); }}
  ></div>
  <div
    class="fixed z-50 bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border rounded-lg shadow-lg py-1 min-w-[180px]"
    style="left: {contextMenu.x}px; top: {contextMenu.y}px;"
  >
    <button
      onclick={() => { handleProcess(contextMenu!.task); closeContextMenu(); }}
      class="w-full flex items-center gap-2 px-3 py-2 text-sm text-gray-700 dark:text-dark-text hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors"
    >
      <Play size={14} class="text-green-500" />
      Start Processing
    </button>
  </div>
{/if}

<svelte:window onkeydown={(e) => { if (e.key === 'Escape') closeContextMenu(); }} />
