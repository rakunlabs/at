<script lang="ts">
  import { push } from 'svelte-spa-router';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    getOrganization,
    getOrganizationBudget,
    updateOrganization,
    listOrgAgents,
    addAgentToOrg,
    removeAgentFromOrg,
    updateOrgAgent,
    submitOrgTask,
    getExportBundleURL,
    previewImportBundle,
    importBundle,
    type Organization,
    type OrganizationAgent,
    type CanvasLayout,
    type IntakeTaskResponse,
    type ContainerConfig,
    type BundlePreview,
    type OrganizationBudgetStatus,
  } from '@/lib/api/organizations';
  import { listAgents, type Agent } from '@/lib/api/agents';
  import { listGoals, type Goal } from '@/lib/api/goals';
  import { TASK_PRIORITIES, TASK_PRIORITY_LABELS, listActiveDelegations, type ActiveDelegation } from '@/lib/api/tasks';
  import { ArrowLeft, Save, Plus, X, RefreshCw, UserPlus, Trash2, Crown, Send, Container, Download, Upload, DollarSign } from 'lucide-svelte';
  import ImportPreviewDialog from '@/lib/components/ImportPreviewDialog.svelte';
  import { agentAvatar } from '@/lib/helper/avatar';
  import OrgChart from '@/lib/components/OrgChart.svelte';
  import BudgetScheduleFields from '@/lib/components/BudgetScheduleFields.svelte';

  // ─── Props ───
  let { params = { id: '' } }: { params?: { id: string } } = $props();

  storeNavbar.title = 'Organization';

  // ─── State ───
  let organization = $state<Organization | null>(null);
  let memberships = $state<OrganizationAgent[]>([]);
  let allAgents = $state<Agent[]>([]);
  let loading = $state(true);
  let saving = $state(false);

  // Editing org info
  let editingOrg = $state(false);
  let editName = $state('');
  let editDescription = $state('');

  // Add-agent panel
  let showAddPanel = $state(false);

  // Submit-task panel
  let showTaskPanel = $state(false);
  let showContainerPanel = $state(false);
  let showBudgetPanel = $state(false);
  let containerConfig = $state<ContainerConfig>({ enabled: false, image: 'at-agent-runtime:latest', cpu: '2', memory: '4g', network: true });
  let budgetMonthlyUsd = $state<number | undefined>(undefined);
  let budgetPeriod = $state('monthly');
  let budgetResetDay = $state(1);
  let budgetResetTime = $state('00:00');
  let budgetTimezone = $state('UTC');
  let organizationBudget = $state<OrganizationBudgetStatus | null>(null);
  let taskTitle = $state('');
  let taskDescription = $state('');
  let taskPriority = $state('');
  let taskGoalId = $state('');
  let submittingTask = $state(false);
  let lastTaskResult = $state<IntakeTaskResponse | null>(null);
  let orgGoals = $state<Goal[]>([]);

  // Selected node
  let selectedAgentId = $state<string | null>(null);

  // Live "currently working" map (only delegations belonging to this org)
  let activeByAgent = $state<Record<string, ActiveDelegation[]>>({});
  let activePollTimer: ReturnType<typeof setInterval> | null = null;

  async function refreshActiveDelegations() {
    if (!params.id) return;
    try {
      const res = await listActiveDelegations();
      const map: Record<string, ActiveDelegation[]> = {};
      for (const d of res.delegations) {
        if (d.org_id !== params.id || !d.agent_id) continue;
        if (!map[d.agent_id]) map[d.agent_id] = [];
        map[d.agent_id].push(d);
      }
      activeByAgent = map;
    } catch {
      activeByAgent = {};
    }
  }

  $effect(() => {
    refreshActiveDelegations();
    activePollTimer = setInterval(refreshActiveDelegations, 5000);
    return () => {
      if (activePollTimer) clearInterval(activePollTimer);
      activePollTimer = null;
    };
  });

  // Bundle import
  let showImportPreview = $state(false);
  let bundlePreview = $state<BundlePreview | null>(null);
  let bundleFile = $state<File | null>(null);
  let importingBundle = $state(false);
  let bundleImportFileInput = $state<HTMLInputElement | undefined>(undefined);

  // ─── Helpers ───

  function agentMap(): Map<string, Agent> {
    return new Map(allAgents.map((a) => [a.id, a]));
  }

  function membershipMap(): Map<string, OrganizationAgent> {
    return new Map(memberships.map((m) => [m.agent_id, m]));
  }

  function formatBudgetReset(value: string): string {
    try {
      return new Date(value).toLocaleString(undefined, {
        year: 'numeric',
        month: 'short',
        day: 'numeric',
        hour: '2-digit',
        minute: '2-digit',
        timeZone: organizationBudget?.budget_timezone || 'UTC',
        timeZoneName: 'short',
      });
    } catch {
      return value;
    }
  }

  function formatBudget(cents: number): string {
    return `$${(cents / 100).toFixed(2)}`;
  }

  // ─── Build OrgChart agent list ───
  function chartAgents() {
    const agents = agentMap();
    return memberships.map((m) => {
      const agent = agents.get(m.agent_id);
      return {
        agent_id: m.agent_id,
        name: agent?.name || m.agent_id,
        description: agent?.config.description,
        title: m.title,
        role: m.role,
        model: agent?.config.model,
        status: m.status,
        parent_agent_id: m.parent_agent_id,
        is_head: organization?.head_agent_id === m.agent_id,
        avatar_seed: agent?.config.avatar_seed,
        active_count: activeByAgent[m.agent_id]?.length || 0,
      };
    });
  }

  // ─── Load ───

  async function loadOrganization() {
    try {
      organization = await getOrganization(params.id);
      storeNavbar.title = `Org: ${organization.name}`;
      if (organization.container_config) {
        containerConfig = { ...containerConfig, ...organization.container_config };
      }
      budgetMonthlyUsd = organization.budget_monthly_cents
        ? organization.budget_monthly_cents / 100
        : undefined;
      try {
        organizationBudget = await getOrganizationBudget(organization.id);
        budgetPeriod = organizationBudget.budget_period;
        budgetResetDay = organizationBudget.budget_reset_day;
        budgetResetTime = organizationBudget.budget_reset_time;
        budgetTimezone = organizationBudget.budget_timezone;
      } catch {
        organizationBudget = null;
        budgetPeriod = organization.budget_period || 'monthly';
        budgetResetDay = organization.budget_reset_day ?? (budgetPeriod === 'daily' ? 0 : 1);
        budgetResetTime = organization.budget_reset_time || '00:00';
        budgetTimezone = organization.budget_timezone || 'UTC';
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load organization', 'alert');
      push('/organizations');
    }
  }

  async function loadMemberships() {
    try {
      memberships = await listOrgAgents(params.id);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load organization agents', 'alert');
    }
  }

  async function loadAllAgents() {
    try {
      const res = await listAgents({ _limit: 1000 });
      allAgents = res.data || [];
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load agents', 'alert');
    }
  }

  async function loadOrgGoals() {
    try {
      const res = await listGoals({ organization_id: params.id, _limit: 200 });
      orgGoals = res.data || [];
    } catch {
      orgGoals = [];
    }
  }

  async function load() {
    loading = true;
    await Promise.all([loadOrganization(), loadMemberships(), loadAllAgents(), loadOrgGoals()]);
    loading = false;
  }

  load();

  // ─── Org Edit ───

  function startEditOrg() {
    if (!organization) return;
    editName = organization.name;
    editDescription = organization.description;
    editingOrg = true;
  }

  async function saveContainerConfig() {
    if (!organization) return;
    try {
      organization = await updateOrganization(organization.id, {
        container_config: containerConfig,
      });
      addToast('Container config saved');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save', 'alert');
    }
  }

  async function saveBudget() {
    if (!organization) return;
    const value = budgetMonthlyUsd ?? 0;
    if (!Number.isFinite(value) || value < 0) {
      addToast('Monthly budget must be zero or a positive USD amount', 'warn');
      return;
    }
    saving = true;
    try {
      organization = await updateOrganization(organization.id, {
        budget_monthly_cents: Math.round(value * 100),
        budget_period: budgetPeriod as 'daily' | 'weekly' | 'monthly',
        budget_reset_day: budgetResetDay,
        budget_reset_time: budgetResetTime,
        budget_timezone: budgetTimezone,
      });
      budgetMonthlyUsd = organization.budget_monthly_cents
        ? organization.budget_monthly_cents / 100
        : undefined;
      addToast(value > 0 ? `Budget set to $${value.toFixed(2)} per ${budgetPeriod} period` : 'Budget disabled');
      try {
        organizationBudget = await getOrganizationBudget(organization.id);
      } catch {
        organizationBudget = null;
      }
      showBudgetPanel = false;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save budget', 'alert');
    } finally {
      saving = false;
    }
  }

  async function saveOrg() {
    if (!organization) return;
    saving = true;
    try {
      organization = await updateOrganization(organization.id, {
        name: editName.trim(),
        description: editDescription.trim(),
      });
      storeNavbar.title = `Org: ${organization.name}`;
      editingOrg = false;
      addToast('Organization updated');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update organization', 'alert');
    } finally {
      saving = false;
    }
  }

  // ─── Head Agent ───

  async function handleHeadAgentChange(e: Event) {
    if (!organization) return;
    const value = (e.target as HTMLSelectElement).value;
    try {
      organization = await updateOrganization(organization.id, { head_agent_id: value });
      addToast(value ? 'Head agent updated' : 'Head agent cleared');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update head agent', 'alert');
    }
  }

  // ─── Submit Task ───

  async function handleSubmitTask() {
    if (!organization || !taskTitle.trim()) return;
    submittingTask = true;
    try {
      const result = await submitOrgTask(organization.id, {
        title: taskTitle.trim(),
        description: taskDescription.trim() || undefined,
        priority_level: taskPriority || undefined,
        goal_id: taskGoalId || undefined,
      });
      lastTaskResult = result;
      taskTitle = '';
      taskDescription = '';
      taskPriority = '';
      taskGoalId = '';
      addToast(`Task ${result.identifier} submitted`);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to submit task', 'alert');
    } finally {
      submittingTask = false;
    }
  }

  // ─── Agent Management ───

  function availableAgents(): Agent[] {
    const memberIds = new Set(memberships.map((m) => m.agent_id));
    return allAgents.filter((a) => !memberIds.has(a.id));
  }

  async function handleAddAgent(agent: Agent) {
    try {
      const result = await addAgentToOrg(params.id, { agent_id: agent.id });
      if ('type' in result && result.type === 'hire_agent' && result.status === 'pending') {
        addToast(`Approval requested to add "${agent.name}"`);
      } else {
        addToast(`Agent "${agent.name}" added to organization`);
        await loadMemberships();
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to add agent', 'alert');
    }
  }

  async function handleRemoveAgent(agentId: string) {
    const agent = allAgents.find((a) => a.id === agentId);
    try {
      await removeAgentFromOrg(params.id, agentId);
      addToast(`Agent "${agent?.name || agentId}" removed from organization`);
      selectedAgentId = null;
      await Promise.all([loadOrganization(), loadMemberships()]);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to remove agent', 'alert');
    }
  }

  async function setAgentParent(agentId: string, parentId: string | null) {
    try {
      await updateOrgAgent(params.id, agentId, { parent_agent_id: parentId || '' });
      addToast('Agent hierarchy updated');
      await loadMemberships();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update hierarchy', 'alert');
    }
  }

  async function handleUpdateHeartbeatSchedule(agentId: string, schedule: string) {
    try {
      await updateOrgAgent(params.id, agentId, { heartbeat_schedule: schedule.trim() });
      addToast('Heartbeat schedule updated');
      await loadMemberships();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update heartbeat schedule', 'alert');
    }
  }

  // ─── Chart selection ───

  function handleChartSelect(agentId: string) {
    selectedAgentId = agentId || null;
  }

  // ─── Computed ───

  function selectedMembership(): OrganizationAgent | null {
    if (!selectedAgentId) return null;
    return memberships.find((m) => m.agent_id === selectedAgentId) || null;
  }

  function selectedAgent(): Agent | null {
    if (!selectedAgentId) return null;
    return allAgents.find((a) => a.id === selectedAgentId) || null;
  }

  // ─── Bundle Export / Import ───

  function handleExportBundle() {
    if (!params.id) return;
    const url = getExportBundleURL(params.id);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${organization?.name || 'organization'}.zip`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    addToast('Downloading organization bundle...');
  }

  async function handleImportBundleFile(event: Event) {
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    bundleFile = file;
    try {
      bundlePreview = await previewImportBundle(file);
      showImportPreview = true;
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to parse bundle', 'alert');
      bundleFile = null;
    }
    input.value = '';
  }

  async function handleConfirmImport(actions: Record<string, string>) {
    if (!bundleFile) return;
    importingBundle = true;
    try {
      const result = await importBundle(bundleFile, actions);
      addToast(`Imported: ${result.agents_imported} agents, ${result.skills_imported} skills, ${result.mcp_sets_imported} MCP sets`);
      showImportPreview = false;
      bundleFile = null;
      bundlePreview = null;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to import bundle', 'alert');
    } finally {
      importingBundle = false;
    }
  }

  function handleCancelImport() {
    showImportPreview = false;
    bundleFile = null;
    bundlePreview = null;
  }
</script>

<svelte:head>
  <title>AT | {organization?.name || 'Organization'}</title>
</svelte:head>

{#if loading}
  <div class="p-8 text-center text-sm text-gray-500 dark:text-dark-text-muted">Loading organization...</div>
{:else if organization}
  <div class="flex flex-col h-full overflow-hidden">
    <!-- Toolbar -->
    <div class="flex items-center justify-between px-3 py-1.5 bg-white dark:bg-dark-surface border-b border-gray-200 dark:border-dark-border shrink-0">
      <div class="flex items-center gap-3">
        <button
          onclick={() => push('/organizations')}
          class="flex items-center gap-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors"
        >
          <ArrowLeft size={14} />
          Back
        </button>
        <div class="h-4 border-l border-gray-200 dark:border-dark-border"></div>
        {#if editingOrg}
          <div class="flex items-center gap-2">
            <input
              type="text"
              bind:value={editName}
              class="text-sm font-medium text-gray-900 dark:text-dark-text bg-transparent border border-gray-300 dark:border-dark-border-subtle rounded px-2 py-0.5 outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 w-48"
              placeholder="Organization name"
            />
            <input
              type="text"
              bind:value={editDescription}
              class="text-xs text-gray-500 dark:text-dark-text-muted bg-transparent border border-gray-300 dark:border-dark-border-subtle rounded px-2 py-0.5 outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 w-64"
              placeholder="Description..."
            />
            <button
              onclick={saveOrg}
              disabled={saving}
              class="flex items-center gap-1 px-2 py-1 text-xs text-white bg-gray-900 dark:bg-accent rounded hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
            >
              <Save size={12} />
              Save
            </button>
            <button
              onclick={() => { editingOrg = false; }}
              class="px-2 py-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors"
            >
              Cancel
            </button>
          </div>
        {:else}
          <div class="flex flex-col">
            <button onclick={startEditOrg} class="text-left group">
              <span class="text-sm font-medium text-gray-900 dark:text-dark-text group-hover:underline">{organization.name}</span>
            </button>
            {#if organization.description}
              <span class="text-[10px] text-gray-400 dark:text-dark-text-faint">{organization.description}</span>
            {/if}
          </div>
        {/if}
      </div>
      <div class="flex items-center gap-2">
        <span class="text-[10px] text-gray-400 dark:text-dark-text-faint">{memberships.length} agent{memberships.length !== 1 ? 's' : ''}</span>
        {#if !editingOrg && memberships.length > 0}
          <div class="h-4 border-l border-gray-200 dark:border-dark-border"></div>
          <div class="flex items-center gap-1.5">
            <Crown size={12} class="text-amber-500" />
            <span class="text-[10px] text-gray-500 dark:text-dark-text-muted">Head:</span>
            <select
              value={organization.head_agent_id || ''}
              onchange={handleHeadAgentChange}
              class="text-xs border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 dark:bg-dark-elevated dark:text-dark-text focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20"
            >
              <option value="">None</option>
              {#each memberships as m (m.agent_id)}
                {@const agent = allAgents.find(a => a.id === m.agent_id)}
                <option value={m.agent_id}>{agent?.name || m.agent_id}</option>
              {/each}
            </select>
          </div>
        {/if}
        <div class="h-4 border-l border-gray-200 dark:border-dark-border"></div>
        <button
          onclick={() => { load(); }}
          class="flex items-center gap-1 px-2 py-1 text-xs text-gray-700 dark:text-dark-text-secondary bg-white dark:bg-dark-surface border border-gray-300 dark:border-dark-border-subtle rounded hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
        >
          <RefreshCw size={12} />
          Refresh
        </button>
        <button
          onclick={handleExportBundle}
          class="flex items-center gap-1 px-2 py-1 text-xs text-gray-700 dark:text-dark-text-secondary bg-white dark:bg-dark-surface border border-gray-300 dark:border-dark-border-subtle rounded hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
          title="Export organization bundle as ZIP"
        >
          <Download size={12} />
          Export
        </button>
        <button
          onclick={() => bundleImportFileInput?.click()}
          class="flex items-center gap-1 px-2 py-1 text-xs text-gray-700 dark:text-dark-text-secondary bg-white dark:bg-dark-surface border border-gray-300 dark:border-dark-border-subtle rounded hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
          title="Import organization bundle from ZIP"
        >
          <Upload size={12} />
          Import
        </button>
        <input
          bind:this={bundleImportFileInput}
          type="file"
          accept=".zip"
          onchange={handleImportBundleFile}
          class="hidden"
        />
        <button
          onclick={() => { showAddPanel = !showAddPanel; showTaskPanel = false; showContainerPanel = false; showBudgetPanel = false; }}
          class="flex items-center gap-1 px-2 py-1 text-xs {showAddPanel ? 'text-white bg-gray-900 dark:bg-accent' : 'text-gray-700 dark:text-dark-text-secondary bg-white dark:bg-dark-surface border border-gray-300 dark:border-dark-border-subtle'} rounded hover:bg-gray-800 dark:hover:bg-accent-hover hover:text-white transition-colors"
        >
          <UserPlus size={12} />
          Add Agent
        </button>
        <button
          onclick={() => { showTaskPanel = !showTaskPanel; showAddPanel = false; showContainerPanel = false; showBudgetPanel = false; }}
          disabled={!organization.head_agent_id}
          title={organization.head_agent_id ? 'Submit a task to this organization' : 'Set a head agent first'}
          class="flex items-center gap-1 px-2 py-1 text-xs {showTaskPanel ? 'text-white bg-gray-900 dark:bg-accent' : 'text-gray-700 dark:text-dark-text-secondary bg-white dark:bg-dark-surface border border-gray-300 dark:border-dark-border-subtle'} rounded hover:bg-gray-800 dark:hover:bg-accent-hover hover:text-white transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <Send size={12} />
          Submit Task
        </button>
        <button
          onclick={() => { showContainerPanel = !showContainerPanel; showAddPanel = false; showTaskPanel = false; showBudgetPanel = false; }}
          class="flex items-center gap-1 px-2 py-1 text-xs {showContainerPanel ? 'text-white bg-gray-900 dark:bg-accent' : 'text-gray-700 dark:text-dark-text-secondary bg-white dark:bg-dark-surface border border-gray-300 dark:border-dark-border-subtle'} rounded hover:bg-gray-800 dark:hover:bg-accent-hover hover:text-white transition-colors"
        >
          <Container size={12} />
          Container
          {#if containerConfig.enabled}
            <span class="w-1.5 h-1.5 bg-green-400 rounded-full"></span>
          {/if}
        </button>
        <button
          onclick={() => { showBudgetPanel = !showBudgetPanel; showAddPanel = false; showTaskPanel = false; showContainerPanel = false; }}
          class="flex items-center gap-1 px-2 py-1 text-xs {showBudgetPanel ? 'text-white bg-gray-900 dark:bg-accent' : 'text-gray-700 dark:text-dark-text-secondary bg-white dark:bg-dark-surface border border-gray-300 dark:border-dark-border-subtle'} rounded hover:bg-gray-800 dark:hover:bg-accent-hover hover:text-white transition-colors"
          title="Configure the organization spending limit"
        >
          <DollarSign size={12} />
          {organization.budget_monthly_cents
            ? `${formatBudget(organizationBudget?.spend_cents || 0)} / ${formatBudget(organization.budget_monthly_cents)}`
            : 'Budget'}
        </button>
      </div>
    </div>

    <!-- Organization Budget Panel -->
    {#if showBudgetPanel}
      {@const spentCents = organizationBudget?.spend_cents || 0}
      {@const limitCents = organizationBudget?.limit_cents ?? organization.budget_monthly_cents ?? 0}
      {@const usagePercent = organizationBudget?.usage_percent ?? (limitCents > 0 ? (spentCents / limitCents) * 100 : 0)}
      <div class="border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface px-4 py-3 shrink-0">
        <div class="flex items-center justify-between mb-3">
          <div>
            <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Organization Budget</span>
            <p class="text-[10px] text-gray-400 dark:text-dark-text-muted mt-0.5">Checked against this organization's cost events before every delegation LLM call.</p>
          </div>
          <button onclick={() => { showBudgetPanel = false; }} class="text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text">
            <X size={14} />
          </button>
        </div>

        <div class="grid grid-cols-4 gap-2 mb-3">
          <div class="border border-gray-200 dark:border-dark-border-subtle bg-gray-50 dark:bg-dark-elevated px-2.5 py-2">
            <span class="text-[9px] font-medium text-gray-400 dark:text-dark-text-muted uppercase tracking-wider block">Spent</span>
            <span class="text-sm font-mono font-medium text-gray-900 dark:text-dark-text">{formatBudget(spentCents)}</span>
          </div>
          <div class="border border-gray-200 dark:border-dark-border-subtle bg-gray-50 dark:bg-dark-elevated px-2.5 py-2">
            <span class="text-[9px] font-medium text-gray-400 dark:text-dark-text-muted uppercase tracking-wider block">Limit</span>
            <span class="text-sm font-mono font-medium text-gray-900 dark:text-dark-text">{limitCents > 0 ? formatBudget(limitCents) : 'Unlimited'}</span>
          </div>
          <div class="border border-gray-200 dark:border-dark-border-subtle bg-gray-50 dark:bg-dark-elevated px-2.5 py-2">
            <span class="text-[9px] font-medium text-gray-400 dark:text-dark-text-muted uppercase tracking-wider block">Remaining</span>
            <span class="text-sm font-mono font-medium" class:text-red-600={limitCents > 0 && spentCents >= limitCents} class:dark:text-red-400={limitCents > 0 && spentCents >= limitCents} class:text-gray-900={limitCents === 0 || spentCents < limitCents} class:dark:text-dark-text={limitCents === 0 || spentCents < limitCents}>
              {limitCents > 0 ? formatBudget(Math.max(0, limitCents - spentCents)) : 'Unlimited'}
            </span>
          </div>
          <div class="border border-gray-200 dark:border-dark-border-subtle bg-gray-50 dark:bg-dark-elevated px-2.5 py-2">
            <span class="text-[9px] font-medium text-gray-400 dark:text-dark-text-muted uppercase tracking-wider block">Period</span>
            <span class="text-[11px] font-medium text-gray-700 dark:text-dark-text-secondary">
              {organizationBudget ? `Resets ${formatBudgetReset(organizationBudget.next_reset_at)}` : 'Unavailable'}
            </span>
          </div>
        </div>

        {#if limitCents > 0}
          <div class="mb-3">
            <div class="flex items-center justify-between text-[10px] mb-1">
              <span class="text-gray-400 dark:text-dark-text-muted">Current {budgetPeriod} period</span>
              <span class="font-mono" class:text-red-600={usagePercent >= 100} class:dark:text-red-400={usagePercent >= 100}>{usagePercent.toFixed(1)}%</span>
            </div>
            <div class="h-1.5 bg-gray-100 dark:bg-dark-elevated overflow-hidden">
              <div
                class="h-full transition-all"
                class:bg-emerald-500={usagePercent < 80}
                class:bg-amber-500={usagePercent >= 80 && usagePercent < 100}
                class:bg-red-500={usagePercent >= 100}
                style="width: {Math.min(100, usagePercent)}%"
              ></div>
            </div>
          </div>
        {/if}

        <div class="mb-3 pb-3 border-b border-gray-100 dark:border-dark-border">
          <BudgetScheduleFields
            bind:period={budgetPeriod}
            bind:resetDay={budgetResetDay}
            bind:resetTime={budgetResetTime}
            bind:timezone={budgetTimezone}
          />
        </div>

        <div class="flex items-end gap-3">
          <label class="block w-56">
            <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Period limit (USD)</span>
            <div class="relative">
              <DollarSign size={12} class="absolute left-2 top-1/2 -translate-y-1/2 text-gray-400 dark:text-dark-text-muted" />
              <input
                type="number"
                min="0"
                step="0.01"
                bind:value={budgetMonthlyUsd}
                placeholder="Unlimited"
                class="w-full pl-6 pr-2 py-1 text-xs font-mono border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
              />
            </div>
          </label>
          <button
            onclick={saveBudget}
            disabled={saving}
            class="flex items-center gap-1 px-3 py-1 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover rounded transition-colors disabled:opacity-50"
          >
            <Save size={12} />
            Save
          </button>
          <span class="pb-1 text-[10px] text-gray-400 dark:text-dark-text-muted">Blank or 0 disables the limit.</span>
        </div>
      </div>
    {/if}

    <!-- Container Config Panel -->
    {#if showContainerPanel}
      <div class="border-b border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface px-4 py-3 shrink-0">
        <div class="flex items-center justify-between mb-3">
          <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Container Isolation</span>
          <button onclick={() => { showContainerPanel = false; }} class="text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text">
            <X size={14} />
          </button>
        </div>

        <div class="grid grid-cols-2 gap-3">
          <!-- Enable toggle -->
          <label class="flex items-center gap-2 col-span-2">
            <input
              type="checkbox"
              bind:checked={containerConfig.enabled}
              class="w-3.5 h-3.5 dark:accent-accent"
            />
            <span class="text-xs text-gray-700 dark:text-dark-text-secondary">Enable Docker container isolation</span>
          </label>

          {#if containerConfig.enabled}
            <!-- Image -->
            <label class="block col-span-2">
              <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Image</span>
              <input
                type="text"
                bind:value={containerConfig.image}
                placeholder="at-agent-runtime:latest"
                class="w-full px-2 py-1 text-xs font-mono border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
              />
            </label>

            <!-- CPU -->
            <label class="block">
              <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">CPU Limit</span>
              <input
                type="text"
                bind:value={containerConfig.cpu}
                placeholder="2"
                class="w-full px-2 py-1 text-xs font-mono border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
              />
            </label>

            <!-- Memory -->
            <label class="block">
              <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Memory Limit</span>
              <input
                type="text"
                bind:value={containerConfig.memory}
                placeholder="4g"
                class="w-full px-2 py-1 text-xs font-mono border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
              />
            </label>

            <!-- Network -->
            <label class="flex items-center gap-2 col-span-2">
              <input
                type="checkbox"
                bind:checked={containerConfig.network}
                class="w-3.5 h-3.5 dark:accent-accent"
              />
              <span class="text-xs text-gray-700 dark:text-dark-text-secondary">Allow network access</span>
            </label>
          {/if}
        </div>

        <div class="flex justify-end mt-3 pt-2 border-t border-gray-100 dark:border-dark-border">
          <button
            onclick={saveContainerConfig}
            class="flex items-center gap-1 px-3 py-1 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover rounded transition-colors"
          >
            <Save size={12} />
            Save
          </button>
        </div>

        {#if containerConfig.enabled}
          <div class="mt-2 text-[10px] text-gray-400 dark:text-dark-text-muted">
            All agents in this org will execute commands inside an isolated Docker container.
            Build the image first: <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1 rounded">docker build -f Dockerfile.agent-runtime -t {containerConfig.image} .</code>
          </div>
        {/if}
      </div>
    {/if}

    <!-- Main area -->
    <div class="flex flex-1 overflow-hidden">
      <!-- Org Chart -->
      <div class="flex-1 relative bg-gray-50 dark:bg-dark-base">
        <OrgChart
          agents={chartAgents()}
          {selectedAgentId}
          onselect={handleChartSelect}
        />

        {#if memberships.length === 0}
          <div class="absolute inset-0 flex items-center justify-center pointer-events-none">
            <div class="text-center">
              <p class="text-sm text-gray-400 dark:text-dark-text-muted">No agents in this organization</p>
              <p class="text-xs text-gray-300 dark:text-dark-text-faint mt-1">Use "Add Agent" to assign agents</p>
            </div>
          </div>
        {/if}
      </div>

      <!-- Submit Task Panel -->
      {#if showTaskPanel}
        <div class="w-64 bg-white dark:bg-dark-surface border-l border-gray-200 dark:border-dark-border shrink-0 min-h-0 flex flex-col">
          <div class="flex items-center justify-between px-3 h-8 border-b border-gray-200 dark:border-dark-border shrink-0">
            <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Submit Task</span>
            <button onclick={() => { showTaskPanel = false; }} class="text-gray-400 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary">
              <X size={14} />
            </button>
          </div>
          <div class="p-3 space-y-3 overflow-y-auto flex-1">
            <label class="block">
              <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Title *</span>
              <input type="text" bind:value={taskTitle} placeholder="What needs to be done?"
                class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text" />
            </label>
            <label class="block">
              <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Description</span>
              <textarea bind:value={taskDescription} rows="3" placeholder="Additional context..."
                class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text resize-y"></textarea>
            </label>
            <label class="block">
              <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Priority</span>
              <select bind:value={taskPriority}
                class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text">
                <option value="">None</option>
                {#each TASK_PRIORITIES as prio}
                  <option value={prio}>{TASK_PRIORITY_LABELS[prio]}</option>
                {/each}
              </select>
            </label>
            {#if orgGoals.length > 0}
              <label class="block">
                <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Goal</span>
                <select bind:value={taskGoalId}
                  class="mt-0.5 w-full px-2 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text">
                  <option value="">None</option>
                  {#each orgGoals as goal}
                    <option value={goal.id}>{goal.name}</option>
                  {/each}
                </select>
              </label>
            {/if}
            <button
              onclick={handleSubmitTask}
              disabled={submittingTask || !taskTitle.trim()}
              class="w-full flex items-center justify-center gap-1.5 px-2 py-1.5 text-xs text-white bg-gray-900 dark:bg-accent rounded hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
            >
              <Send size={12} />
              {submittingTask ? 'Submitting...' : 'Submit'}
            </button>
            {#if lastTaskResult}
              <div class="p-2 bg-green-50 dark:bg-green-900/20 border border-green-200 dark:border-green-800 rounded text-xs">
                <span class="font-medium text-green-700 dark:text-green-400">{lastTaskResult.identifier}</span>
                <span class="text-green-600 dark:text-green-500"> created — delegation in progress</span>
              </div>
            {/if}
          </div>
        </div>
      {/if}

      <!-- Add Agent Panel -->
      {#if showAddPanel}
        <div class="w-64 bg-white dark:bg-dark-surface border-l border-gray-200 dark:border-dark-border shrink-0 min-h-0 flex flex-col">
          <div class="flex items-center justify-between px-3 h-8 border-b border-gray-200 dark:border-dark-border shrink-0">
            <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Add Agent</span>
            <button onclick={() => { showAddPanel = false; }} class="text-gray-400 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary">
              <X size={14} />
            </button>
          </div>
          <div class="overflow-y-auto min-h-0 flex-1">
            {#if availableAgents().length === 0}
              <div class="p-3 text-xs text-gray-400 dark:text-dark-text-faint text-center">
                All agents are already in this organization
              </div>
            {:else}
              {#each availableAgents() as agent (agent.id)}
                <div
                  class="flex items-center justify-between px-3 py-2 border-b border-gray-100 dark:border-dark-border hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
                >
                  <div class="flex items-center gap-2 min-w-0">
                    <img src={agentAvatar(agent.config.avatar_seed, agent.name, 24)} alt="" class="w-6 h-6 rounded-full shrink-0 bg-gray-100 dark:bg-dark-elevated" />
                    <div class="min-w-0">
                      <div class="text-xs font-medium text-gray-800 dark:text-dark-text truncate">{agent.name}</div>
                      {#if agent.config.model}
                        <div class="text-[10px] text-gray-400 dark:text-dark-text-faint font-mono truncate">{agent.config.model}</div>
                      {/if}
                    </div>
                  </div>
                  <button
                    onclick={() => handleAddAgent(agent)}
                    class="shrink-0 ml-2 p-1 text-gray-400 dark:text-dark-text-muted hover:text-green-600 dark:hover:text-green-400 hover:bg-green-50 dark:hover:bg-green-900/20 rounded transition-colors"
                    title="Add to organization"
                  >
                    <Plus size={14} />
                  </button>
                </div>
              {/each}
            {/if}
          </div>
        </div>
      {/if}

      <!-- Agent Detail Panel -->
      {#if selectedAgentId && selectedMembership()}
        {@const membership = selectedMembership()}
        {@const agent = selectedAgent()}
        <div class="w-60 bg-white dark:bg-dark-surface border-l border-gray-200 dark:border-dark-border shrink-0 min-h-0 flex flex-col">
          <div class="flex items-center justify-between px-3 h-8 border-b border-gray-200 dark:border-dark-border shrink-0">
            <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Agent Details</span>
            <button onclick={() => { selectedAgentId = null; }} class="text-gray-400 dark:text-dark-text-faint hover:text-gray-600 dark:hover:text-dark-text-secondary">
              <X size={14} />
            </button>
          </div>
          <div class="p-3 space-y-3 overflow-y-auto min-h-0 flex-1">
            {#if membership && agent}
              <div class="flex items-center gap-2.5">
                <img src={agentAvatar(agent.config.avatar_seed, agent.name, 36)} alt="" class="w-9 h-9 rounded-full shrink-0 bg-gray-100 dark:bg-dark-elevated" />
                <div>
                  <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Name</span>
                  <div class="text-xs font-medium text-gray-900 dark:text-dark-text mt-0.5">{agent.name}</div>
                </div>
              </div>
              {#if membership.title}
                <div>
                  <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Title</span>
                  <div class="text-xs text-gray-700 dark:text-dark-text-secondary mt-0.5">{membership.title}</div>
                </div>
              {/if}
              {#if membership.role}
                <div>
                  <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Role</span>
                  <div class="text-xs text-gray-700 dark:text-dark-text-secondary mt-0.5">{membership.role}</div>
                </div>
              {/if}
              {#if agent.config.model}
                <div>
                  <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Model</span>
                  <div class="text-xs text-gray-700 dark:text-dark-text-secondary font-mono mt-0.5">{agent.config.model}</div>
                </div>
              {/if}
              {#if agent.config.description}
                <div>
                  <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Description</span>
                  <div class="text-xs text-gray-600 dark:text-dark-text-muted mt-0.5">{agent.config.description}</div>
                </div>
              {/if}

              <!-- Parent selector -->
              <div>
                <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Reports To</span>
                <select
                  value={membership.parent_agent_id || ''}
                  onchange={(e) => setAgentParent(membership.agent_id, (e.target as HTMLSelectElement).value || null)}
                  class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text"
                >
                  <option value="">None (root)</option>
                  {#each memberships.filter((m) => m.agent_id !== membership.agent_id) as candidate (candidate.agent_id)}
                    {@const candidateAgent = allAgents.find((a) => a.id === candidate.agent_id)}
                    <option value={candidate.agent_id}>{candidateAgent?.name || candidate.agent_id}</option>
                  {/each}
                </select>
              </div>

              <!-- Heartbeat schedule -->
              <div>
                <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Heartbeat Schedule</span>
                <input
                  type="text"
                  value={membership.heartbeat_schedule || ''}
                  onchange={(e) => handleUpdateHeartbeatSchedule(membership.agent_id, (e.target as HTMLInputElement).value)}
                  placeholder="Cron (e.g., */5 * * * *)"
                  class="mt-0.5 w-full px-2 py-1 text-xs font-mono border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted"
                />
              </div>

            {:else if membership}
              <div class="text-xs text-gray-400 dark:text-dark-text-faint">
                Agent data unavailable (may have been deleted)
              </div>
            {/if}
          </div>
          <div class="px-3 py-2 border-t border-gray-200 dark:border-dark-border shrink-0">
            <button
              onclick={() => { if (selectedAgentId) handleRemoveAgent(selectedAgentId); }}
              class="w-full flex items-center justify-center gap-1 px-2 py-1 text-xs text-red-600 dark:text-red-400 border border-red-200 dark:border-red-800 rounded hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors"
            >
              <Trash2 size={12} />
              Remove from Org
            </button>
          </div>
        </div>
      {/if}
    </div>
  </div>
{/if}

{#if showImportPreview && bundlePreview}
  <ImportPreviewDialog
    preview={bundlePreview}
    onconfirm={handleConfirmImport}
    oncancel={handleCancelImport}
    importing={importingBundle}
  />
{/if}
