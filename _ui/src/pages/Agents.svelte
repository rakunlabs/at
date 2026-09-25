<script lang="ts">
  import BuiltinToolPicker from '@/lib/components/BuiltinToolPicker.svelte';
  import { builtinDisabledBy } from '@/lib/helper/builtin-tools';
  import { onMount, untrack } from 'svelte';
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { createAgent, updateAgent, deleteAgent, publishAgent, exportAgent, importAgent, type Agent, type AgentScope } from '@/lib/api/agents';
  import { isNativeAdmin, storeAuth } from '@/lib/store/auth.svelte';
  import { listActiveDelegations, type ActiveDelegation } from '@/lib/api/tasks';
  import { getAgentBudget, setAgentBudget, type AgentBudget } from '@/lib/api/agent-budgets';
  import { Trash2, Plus, X, Pencil, Bot, RefreshCw, RefreshCcw, Save, Copy, ClipboardPaste, Wrench, ShieldCheck, Download, Upload, Share2, Workflow as WorkflowIcon } from 'lucide-svelte';
  import { agentAvatar, generateAvatar } from '@/lib/helper/avatar';
  import { toggleSort } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';
  import BudgetScheduleFields from '@/lib/components/BudgetScheduleFields.svelte';
  import AgentBuilderPanel from '@/lib/components/AgentBuilderPanel.svelte';
  import LoadIssues from '@/lib/components/LoadIssues.svelte';
  import { createAgentPage } from '@/lib/helper/agent-page.svelte';
  import { isFeatureEnabled, loadFeatures, storeFeatures } from '@/lib/store/features.svelte';
  const page = createAgentPage();
  import { applyAgentDraftPatch, type AgentDraft, type AgentBuilderCatalog } from '@/lib/helper/agent-builder';
  import { can, workspaceState } from '@/lib/store/workspace.svelte';

  storeNavbar.title = 'Agents';

  // ─── State ───

  let agents = $derived(page.data.agents);
  let providers = $derived(page.data.providers);
  let skills = $derived(page.data.skills);
  let mcpSets = $derived(page.data.mcpSets);
  let workflows = $derived(page.data.workflows);
  let builtinToolDefs = $derived(page.data.builtinToolDefs);
  let connections = $derived(page.data.connections);
  let loading = $derived(page.list.loading('Agents'));
  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let saving = $state(false);
  let showAIBuilder = $state(false);
  let builderBusy = $state(false);
  let formVersion = $state(0);
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);
  
  // Pagination (client-side: provider/model/group live inside the JSON config
  // column, which the generic list query can't ORDER BY — sorting by them
  // server-side 500s. The agent set is small, so we load all and filter/sort/
  // paginate in the browser.)
  let offset = $state(0);
  let limit = $state(25);

  // Live "currently working" map: agent_id → list of active delegations
  let activeByAgent = $state<Record<string, ActiveDelegation[]>>({});
  $effect(() => {
    // This endpoint is installation-admin-only. Unknown/disabled features must
    // not create a timer. A feature toggle tears down the existing poller.
    if (!storeFeatures.loaded || !isFeatureEnabled('tasks') || !isNativeAdmin()) {
      activeByAgent = {};
      return;
    }
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    async function refresh() {
      try {
        const res = await listActiveDelegations();
        if (stopped) return;
        const map: Record<string, ActiveDelegation[]> = {};
        for (const d of res.delegations || []) {
          if (d.agent_id) (map[d.agent_id] ||= []).push(d);
        }
        activeByAgent = map;
      } catch (error: any) {
        if (stopped) return;
        activeByAgent = {};
        // The catalog may be stale after another administrator changed it.
        if ([401, 403, 404].includes(error?.response?.status)) return;
      }
      if (!stopped) timer = setTimeout(refresh, 5000);
    }
    void refresh();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  });

  $effect(() => {
    if (showForm) untrack(() => { void page.loadEditor(); });
    return () => page.closeEditor();
  });

  // Form fields
  let formName = $state('');
  let formDescription = $state('');
  let formGroup = $state('');
  let formProvider = $state('');
  let formModel = $state('');
  let formReasoningEffort = $state('');
  let formSystemPrompt = $state('');
  let formSkills = $state<string[]>([]);
  let formMCPSets = $state<string[]>([]);
  let formWorkflows = $state<string[]>([]);
  let formBuiltinTools = $state<string[]>([]);
  let formSubagents = $state<string[]>([]);
  let formMCPs = $state<string[]>(['']);
  let formMaxIterations = $state(10);
  let formToolTimeout = $state(60);
  let formConfirmationTools = $state<string[]>([]);
  let formAvatarSeed = $state('');
  let showAvatarSeed = $state(false);
  // Ownership tier. Chosen at creation; there is no tier conversion, so the
  // selector is disabled while editing. "global" is offered to installation
  // administrators only (the server refuses it for everyone else anyway).
  let formScope = $state<AgentScope>('personal');
  let mayPublish = $derived(isNativeAdmin() || can('agents.write'));
  let formAgentBudget = $state<AgentBudget | null>(null);
  let formBudgetLimit = $state<number | undefined>(undefined);
  let formBudgetPeriod = $state('monthly');
  let formBudgetResetDay = $state(1);
  let formBudgetResetTime = $state('00:00');
  let formBudgetTimezone = $state('UTC');
  /**
   * provider → connection_id. Agents bind a default connection per provider;
   * tool handlers resolve provider-scoped variable keys (e.g. youtube_refresh_token)
   * through this map before falling back to global variables.
   */
  let formConnections = $state<Record<string, string>>({});

  function getAgentDraft(): AgentDraft {
    return {
      name: formName, description: formDescription, group: formGroup,
      provider: formProvider, model: formModel, reasoning_effort: formReasoningEffort,
      system_prompt: formSystemPrompt, skills: [...formSkills], mcp_sets: [...formMCPSets],
      workflows: [...formWorkflows], builtin_tools: [...formBuiltinTools],
      max_iterations: formMaxIterations, tool_timeout: formToolTimeout,
    };
  }

  function getBuilderCatalog(): AgentBuilderCatalog {
    // Project only form choices. Never send provider configs or connection secrets.
    return {
      providers: providers.filter(p => !p.config.disabled).map(p => ({ key: p.key, type: p.config.type, models: [...(p.config.models || [])], default_model: p.config.model || '' })),
      skills: skills.map(s => ({ id: s.id, name: s.name, description: s.description })),
      mcp_sets: mcpSets.map(s => ({ id: s.id, name: s.name })),
      workflows: workflows.map(w => ({ id: w.id, name: w.name })),
      builtin_tools: builtinToolDefs.filter(t => !builtinDisabledBy(t, isFeatureEnabled)).map(t => ({ id: t.name, name: t.name, description: t.description })),
    };
  }

  function applyBuilderPatch(patch: unknown): string[] {
    if (!showForm || saving) throw new Error('The agent form is not available for changes.');
    const before = getAgentDraft();
    const draft = applyAgentDraftPatch(before, patch, getBuilderCatalog());
    formName = draft.name; formDescription = draft.description; formGroup = draft.group;
    formProvider = draft.provider; formModel = draft.model; formReasoningEffort = draft.reasoning_effort;
    formSystemPrompt = draft.system_prompt; formSkills = draft.skills; formMCPSets = draft.mcp_sets;
    formWorkflows = draft.workflows; formBuiltinTools = draft.builtin_tools;
    formMaxIterations = draft.max_iterations; formToolTimeout = draft.tool_timeout;
    return (Object.keys(draft) as (keyof AgentDraft)[]).filter(key => JSON.stringify(before[key]) !== JSON.stringify(draft[key]));
  }

  // Copy / Paste via system clipboard
  
  async function copyAgent(agent: Agent) {
    const exportData = {
      name: agent.name,
      config: {
        description: agent.config.description,
        group: agent.config.group || '',
        provider: agent.config.provider,
        model: agent.config.model,
        reasoning_effort: agent.config.reasoning_effort ?? '',
        system_prompt: agent.config.system_prompt,
        skills: agent.config.skills || [],
        mcp_sets: agent.config.mcp_sets || [],
        workflows: agent.config.workflows || [],
        builtin_tools: agent.config.builtin_tools || [],
        subagents: agent.config.subagents || [],
        mcp_urls: agent.config.mcp_urls || [],
        max_iterations: agent.config.max_iterations,
        tool_timeout: agent.config.tool_timeout,
        confirmation_required_tools: agent.config.confirmation_required_tools || [],
        avatar_seed: agent.config.avatar_seed || '',
        connections: agent.config.connections || {},
      },
    };
    try {
      await navigator.clipboard.writeText(JSON.stringify(exportData, null, 2));
      addToast(`Copied "${agent.name}" to clipboard`);
    } catch {
      addToast('Failed to copy to clipboard', 'alert');
    }
  }

  async function pasteAgent() {
    try {
      const text = await navigator.clipboard.readText();
      const src = JSON.parse(text);
      if (!src.name || typeof src.name !== 'string') {
        addToast('Clipboard does not contain a valid agent', 'warn');
        return;
      }
      resetForm();
      formName = src.name + '_copy';
      // Support both old flat format and new nested config format
      const cfg = src.config || src;
      formDescription = cfg.description || '';
      formGroup = cfg.group || '';
      formProvider = cfg.provider || '';
      formModel = cfg.model || '';
      formReasoningEffort = cfg.reasoning_effort ?? '';
      formSystemPrompt = cfg.system_prompt || '';
      formSkills = (cfg.skills || []).map((s: any) => (typeof s === 'string' ? s : s?.id ?? ''));
      formMCPSets = cfg.mcp_sets || [];
      formWorkflows = cfg.workflows || [];
      formConnections = cfg.connections || {};
      formBuiltinTools = cfg.builtin_tools || [];
      formSubagents = cfg.subagents || [];
      formMCPs = cfg.mcp_urls && cfg.mcp_urls.length > 0 ? [...cfg.mcp_urls] : [''];
      formMaxIterations = cfg.max_iterations || 10;
      formToolTimeout = cfg.tool_timeout || 60;
      formConfirmationTools = cfg.confirmation_required_tools || [];
      formAvatarSeed = cfg.avatar_seed || '';
      editingId = null;
      showForm = true;
    } catch {
      addToast('Nothing to paste — copy an agent first or check clipboard permissions', 'warn');
    }
  }

  // Export agent as .md file download
  async function handleExport(agent: Agent) {
    try {
      const mdContent = await exportAgent(agent.id);
      const blob = new Blob([mdContent], { type: 'text/markdown' });
      const url = URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = `${agent.name}.md`;
      document.body.appendChild(a);
      a.click();
      document.body.removeChild(a);
      URL.revokeObjectURL(url);
      addToast(`Exported "${agent.name}" as ${agent.name}.md`);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to export agent', 'alert');
    }
  }

  // Import agent from .md file
  let importFileInput: HTMLInputElement;
  async function handleImportFile(event: Event) {
    const input = event.target as HTMLInputElement;
    const file = input.files?.[0];
    if (!file) return;
    try {
      const content = await file.text();
      await importAgent(content, 'personal');
      addToast(`Imported agent from "${file.name}"`);
      await loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to import agent', 'alert');
    }
    input.value = '';
  }

  // ─── Load ───

  async function loadData() {
    await page.loadList();
    // Deleting the last row of a later page must not strand the remaining rows
    // behind an empty table with no pagination controls.
    offset = 0;
  }

  function handleSearch(value: string) {
    searchQuery = value;
    offset = 0;
  }

  function handleSort(field: string, multiSort: boolean) {
    sorts = toggleSort(sorts, field, multiSort);
    offset = 0;
  }

  // ─── Client-side filter / sort / paginate ───

  function agentSortValue(a: Agent, field: string): string {
    switch (field) {
      case 'name': return a.name || '';
      case 'provider': return a.config.provider || '';
      case 'model': return a.config.model || '';
      case 'group': return a.config.group || '';
      default: return '';
    }
  }

  let filteredAgents = $derived.by(() => {
    const q = searchQuery.trim().toLowerCase();
    if (!q) return agents;
    return agents.filter((a) => {
      const hay = [
        a.name,
        a.config.group,
        a.config.provider,
        a.config.model,
        a.config.description,
      ].filter(Boolean).join(' ').toLowerCase();
      return hay.includes(q);
    });
  });

  let sortedAgents = $derived.by(() => {
    if (sorts.length === 0) return filteredAgents;
    const list = [...filteredAgents];
    list.sort((a, b) => {
      for (const s of sorts) {
        const cmp = agentSortValue(a, s.field).localeCompare(
          agentSortValue(b, s.field), undefined, { sensitivity: 'base', numeric: true },
        );
        if (cmp !== 0) return s.desc ? -cmp : cmp;
      }
      return 0;
    });
    return list;
  });

  let filteredTotal = $derived(filteredAgents.length);
  let pagedAgents = $derived(sortedAgents.slice(offset, offset + limit));

  // Distinct, sorted group names for the form datalist (reuse existing labels).
  let existingGroups = $derived.by(() => {
    const set = new Set<string>();
    for (const a of agents) {
      const g = (a.config.group || '').trim();
      if (g) set.add(g);
    }
    return Array.from(set).sort((x, y) => x.localeCompare(y));
  });

  onMount(() => {
    void loadData();
    void loadFeatures().catch(() => {});
  });

  // ─── Form ───

  function resetForm() {
    formVersion++;
    showAIBuilder = false;
    builderBusy = false;
    formName = '';
    formDescription = '';
    formGroup = '';
    formProvider = '';
    formModel = '';
    formReasoningEffort = '';
    formSystemPrompt = '';
    formSkills = [];
    formMCPSets = [];
    formWorkflows = [];
    formBuiltinTools = [];
    formSubagents = [];
    formMCPs = [''];
    formMaxIterations = 10;
    formToolTimeout = 60;
    formConfirmationTools = [];
    formAvatarSeed = '';
    showAvatarSeed = false;
    formAgentBudget = null;
    formBudgetLimit = undefined;
    formBudgetPeriod = 'monthly';
    formBudgetResetDay = 1;
    formBudgetResetTime = '00:00';
    formBudgetTimezone = 'UTC';
    formConnections = {};
    formScope = 'personal';
    editingId = null;
    showForm = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  async function openEdit(agent: Agent) {
    resetForm();
    editingId = agent.id;
    formName = agent.name;
    formDescription = agent.config.description;
    formGroup = agent.config.group || '';
    formProvider = agent.config.provider;
    formModel = agent.config.model;
    formReasoningEffort = agent.config.reasoning_effort ?? '';
    formSystemPrompt = agent.config.system_prompt;
    formSkills = (agent.config.skills || []).map((s) => (typeof s === 'string' ? s : s.id));
    formMCPSets = [...(agent.config.mcp_sets || [])];
    formWorkflows = [...(agent.config.workflows || [])];
    formBuiltinTools = [...(agent.config.builtin_tools || [])];
    formSubagents = [...(agent.config.subagents || [])];
    formMCPs = agent.config.mcp_urls && agent.config.mcp_urls.length > 0 ? [...agent.config.mcp_urls] : [''];
    formMaxIterations = agent.config.max_iterations || 10;
    formToolTimeout = agent.config.tool_timeout || 60;
    formConfirmationTools = [...(agent.config.confirmation_required_tools || [])];
    formAvatarSeed = agent.config.avatar_seed || '';
    formConnections = { ...(agent.config.connections || {}) };
    formScope = agent.scope || 'workspace';
    showForm = true;
    const requestedAgentID = agent.id;
    if (!isNativeAdmin()) return;
    try {
      const budget = await getAgentBudget(agent.id);
      if (editingId !== requestedAgentID) return;
      formAgentBudget = budget;
      if (formAgentBudget) {
        formBudgetLimit = formAgentBudget.monthly_limit || undefined;
        formBudgetPeriod = formAgentBudget.budget_period || 'monthly';
        formBudgetResetDay = formAgentBudget.budget_reset_day || (formBudgetPeriod === 'daily' ? 0 : 1);
        formBudgetResetTime = formAgentBudget.budget_reset_time || '00:00';
        formBudgetTimezone = formAgentBudget.budget_timezone || 'UTC';
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load agent budget', 'alert');
    }
  }

  async function handleSubmit() {
    if (saving || builderBusy) return;
    if (!formName.trim()) {
      addToast('Agent name is required', 'warn');
      return;
    }
    if (!formProvider) {
      addToast('Provider is required', 'warn');
      return;
    }
    if (reasoningEffortError) {
      addToast(reasoningEffortError, 'warn');
      return;
    }

    saving = true;
    try {
      const cleanMCPs = formMCPs.filter(u => u.trim() !== '');
      const payload = {
        name: formName.trim(),
        config: {
          description: formDescription.trim(),
          group: formGroup.trim() || undefined,
          provider: formProvider,
          model: formModel,
          reasoning_effort: formReasoningEffort,
          system_prompt: formSystemPrompt,
          skills: formSkills,
          mcp_sets: formMCPSets,
          workflows: formWorkflows,
          builtin_tools: formBuiltinTools,
          subagents: formSubagents,
          mcp_urls: cleanMCPs,
          max_iterations: formMaxIterations,
          tool_timeout: formToolTimeout,
          confirmation_required_tools: formConfirmationTools,
          avatar_seed: formAvatarSeed || undefined,
          connections: Object.keys(formConnections).length > 0 ? formConnections : undefined,
          // The flag must survive edits of a global agent: config is sent
          // wholesale, so omitting it would silently unshare on every save.
          shared_with_all_workspaces: formScope === 'global' ? true : undefined,
        },
        scope: editingId ? undefined : formScope,
      };

      let savedAgent: Agent;
      if (editingId) {
        savedAgent = await updateAgent(editingId, payload);
        addToast(`Agent "${formName}" updated`);
      } else {
        savedAgent = await createAgent(payload);
        editingId = savedAgent.id;
        addToast(`Agent "${formName}" created`);
      }
      if (formBudgetLimit !== undefined || formAgentBudget !== null) {
        await setAgentBudget(savedAgent.id, {
          monthly_limit: formBudgetLimit ?? 0,
          budget_period: formBudgetPeriod as 'daily' | 'weekly' | 'monthly',
          budget_reset_day: formBudgetResetDay,
          budget_reset_time: formBudgetResetTime,
          budget_timezone: formBudgetTimezone,
        });
      }
      resetForm();
      await loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save agent', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteAgent(id);
      addToast('Agent deleted');
      deleteConfirm = null;
      await loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete agent', 'alert');
    }
  }

  async function handlePublish(agent: Agent) {
    if (!confirm(`Copy "${agent.name}" to the workspace?`)) return;
    try {
      await publishAgent(agent.id);
      addToast(`Agent "${agent.name}" copied to the workspace`);
      await loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to publish agent', 'alert');
    }
  }

  function canManageAgent(agent: Agent): boolean {
    if (agent.workspace_id && agent.workspace_id !== workspaceState.access?.workspace_id) return false;
    if (isNativeAdmin()) return true;
    return agent.owner_user_id
      ? agent.owner_user_id === storeAuth.identity?.subject
      : mayPublish;
  }

  // ─── MCP Management ───

  function addMcpInput() {
    formMCPs = [...formMCPs, ''];
  }

  function removeMcpInput(i: number) {
    formMCPs = formMCPs.filter((_, idx) => idx !== i);
  }

  function updateMcpInput(i: number, val: string) {
    formMCPs[i] = val;
  }

  // ─── Derived ───

  let selectedProviderConfig = $derived(providers.find(p => p.key === formProvider));
  let providerGroups = $derived([
    { label: 'Personal', providers: providers.filter(p => p.scope === 'personal') },
    { label: 'Workspace', providers: providers.filter(p => !p.scope || p.scope === 'workspace') },
    { label: 'Global', providers: providers.filter(p => p.scope === 'global') },
  ].filter(group => group.providers.length > 0));
  let reasoningAdapterType = $derived(selectedProviderConfig?.config.type ?? '');
  let forwardsReasoningEffort = $derived(['openai', 'azure', 'vertex'].includes(reasoningAdapterType));
  let mapsThinkingBudget = $derived(['anthropic', 'gemini', 'vertex-gemini', 'minimax'].includes(reasoningAdapterType));
  let reasoningUnsupported = $derived(['bedrock', 'cohere'].includes(reasoningAdapterType));
  let reasoningEffortOptions = $derived(
    forwardsReasoningEffort ? ['low', 'medium', 'high', 'xhigh']
      : mapsThinkingBudget ? ['low', 'medium', 'high'] : []
  );
  let unlistedReasoningEffort = $derived(formReasoningEffort !== '' && !reasoningEffortOptions.includes(formReasoningEffort));
  let reasoningEffortError = $derived(
    !['', 'low', 'medium', 'high', 'xhigh'].includes(formReasoningEffort)
      ? 'Invalid reasoning effort. Choose Default or a listed effort before saving.'
      : unlistedReasoningEffort && (forwardsReasoningEffort || mapsThinkingBudget || reasoningUnsupported)
        ? 'This adapter does not support the selected reasoning effort. Choose Default or a supported effort before saving.'
        : ''
  );
  let availableModels = $derived(
    selectedProviderConfig?.config?.models?.length
      ? selectedProviderConfig.config.models
      : selectedProviderConfig?.config?.model
        ? [selectedProviderConfig.config.model]
        : []
  );
</script>

<svelte:head>
  <title>AT | Agents</title>
</svelte:head>

<div class="flex h-full min-h-0 min-w-0 overflow-hidden">
  <div class="min-h-0 min-w-0 flex-1 overflow-y-auto overscroll-contain">
    <div class="p-6 max-w-6xl mx-auto">
      {#if agents.length > 0}
        <LoadIssues issues={page.list.issues} retry={loadData} {loading} />
      {/if}
      <!-- Header -->
      <div class="flex items-center justify-between mb-4">
        <div class="flex items-center gap-2">
          <Bot size={16} class="text-gray-500 dark:text-dark-text-muted" />
          <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Agents</h2>
          <span class="text-xs text-gray-400 dark:text-dark-text-muted">({filteredTotal})</span>
          {#if Object.keys(activeByAgent).length > 0}
            {@const totalActive = Object.values(activeByAgent).reduce((s, a) => s + a.length, 0)}
            <span class="flex items-center gap-1.5 ml-2 px-2 py-0.5 text-[10px] font-medium bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-400 border border-green-200 dark:border-green-900/40" title="Active delegations right now">
              <span class="relative flex w-1.5 h-1.5">
                <span class="absolute inline-flex w-full h-full rounded-full bg-green-400 opacity-75 animate-ping"></span>
                <span class="relative inline-flex w-1.5 h-1.5 rounded-full bg-green-500"></span>
              </span>
              {totalActive} working
            </span>
          {/if}
        </div>
        <div class="flex items-center gap-2">
          <button
            onclick={loadData}
            class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary "
            title="Refresh"
          >
            <RefreshCw size={14} />
          </button>
          <button
            onclick={() => importFileInput.click()}
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-50 dark:hover:bg-dark-elevated "
            title="Import agent from .md file"
          >
            <Upload size={12} />
            Import
          </button>
          <input
            bind:this={importFileInput}
            type="file"
            accept=".md"
            onchange={handleImportFile}
            class="hidden"
          />
          <button
            onclick={openCreate}
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover "
          >
            <Plus size={12} />
            New Agent
          </button>
        </div>
      </div>

      <!-- Inline Form -->
      {#if showForm}
        <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
          <div class="flex flex-wrap items-center justify-between gap-2 px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50">
            <div class="flex flex-wrap items-center gap-2">
              <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
                {editingId ? `Edit: ${formName}` : 'New Agent'}
              </span>
              {#if !editingId}
                <button
                  type="button"
                  onclick={pasteAgent}
                  class="flex items-center gap-1 px-2 py-1 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated hover:text-gray-900 dark:hover:text-dark-text "
                  title="Paste agent from clipboard"
                >
                  <ClipboardPaste size={12} />
                  Paste
                </button>
              {/if}
              <button type="button" disabled={saving} aria-expanded={showAIBuilder} aria-controls="agent-ai-builder" onclick={() => { showAIBuilder = !showAIBuilder; if (!showAIBuilder) builderBusy = false; }} class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium border {showAIBuilder ? 'bg-accent-muted text-accent dark:text-accent-text border-accent/30' : 'border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated'}"><Bot size={14} />AI Builder</button>
            </div>
            <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary ">
              <X size={14} />
            </button>
          </div>

          <form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="min-w-0 p-4 space-y-4">
            <LoadIssues issues={page.editor.issues} retry={page.loadEditor} loading={page.editorLoading} />
            <!-- Profile Header: Avatar left, identity fields right -->
            <div class="flex flex-col sm:flex-row gap-4 items-start">
              <!-- Avatar (large, left side) -->
              <div class="group relative shrink-0 w-24 h-24">
                <img
                  src={generateAvatar(formAvatarSeed || formName || 'agent', 200)}
                  alt="Agent avatar"
                  class="w-24 h-24 bg-gray-100 dark:bg-dark-elevated border border-gray-200 dark:border-dark-border"
                />
                <!-- Overlay buttons — visible on hover -->
                <div class="absolute top-1.5 right-1.5 flex gap-1 opacity-0 group-hover:opacity-100 ">
                  <button
                    type="button"
                    onclick={() => { showAvatarSeed = !showAvatarSeed; }}
                    class="p-1 bg-black/30 hover:bg-black/50 text-white/70 hover:text-white "
                    title="Custom seed"
                  >
                    <Pencil size={13} />
                  </button>
                  <button
                    type="button"
                    onclick={() => { formAvatarSeed = (formName || 'agent') + '_' + Math.random().toString(36).slice(2, 8); }}
                    class="p-1 bg-black/30 hover:bg-black/50 text-white/70 hover:text-white "
                    title="Randomize avatar"
                  >
                    <RefreshCcw size={13} />
                  </button>
                </div>
                <!-- Seed input — overlaid at bottom of avatar -->
                {#if showAvatarSeed}
                  <div class="absolute bottom-0 left-0 right-0 bg-black/40 px-2 py-1.5">
                    <input
                      type="text"
                      bind:value={formAvatarSeed}
                      placeholder="Custom seed..."
                      class="w-full bg-black/30 border border-white/20 px-2 py-1 text-xs text-white placeholder:text-white/50 focus:outline-none focus:border-white/40"
                    />
                  </div>
                {/if}
              </div>

              <!-- Identity fields (right side) -->
              <div class="w-full min-w-0 flex-1 space-y-3">
                <!-- Name -->
                <div>
                  <label for="form-name" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Name</label>
                  <input
                    id="form-name"
                    type="text"
                    bind:value={formName}
                    placeholder="e.g., code_reviewer, data_analyst"
                    class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
                  />
                </div>

                <!-- Ownership tier (chosen at creation; no tier conversion) -->
                <div>
                  <label for="form-scope" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Availability</label>
                  <select
                    id="form-scope"
                    bind:value={formScope}
                    disabled={!!editingId}
                    class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text disabled:opacity-60"
                  >
                    <option value="personal">Personal — only visible to you</option>
                    {#if mayPublish || formScope === 'workspace'}
                      <option value="workspace">Workspace — shared with this workspace</option>
                    {/if}
                    {#if isNativeAdmin() || formScope === 'global'}
                      <option value="global">Global — available in every workspace</option>
                    {/if}
                  </select>
                  {#if !editingId}
                    <p class="mt-1 text-[10px] text-gray-400 dark:text-dark-text-muted">Create it privately first, then copy it to the workspace when ready. Global requires an installation administrator in the Default workspace.</p>
                  {/if}
                </div>

                <!-- Description -->
                <div>
                  <label for="form-description" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Description</label>
                  <input
                    id="form-description"
                    type="text"
                    bind:value={formDescription}
                    placeholder="What this agent does"
                    class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
                  />
                </div>

                <!-- Group + Provider + Model -->
                <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
                  <div>
                    <label for="form-group" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Group</label>
                    <input
                      id="form-group"
                      type="text"
                      list="agent-group-options"
                      bind:value={formGroup}
                      placeholder="e.g. YouTube Shorts"
                      class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
                    />
                    <datalist id="agent-group-options">
                      {#each existingGroups as g}
                        <option value={g}></option>
                      {/each}
                    </datalist>
                  </div>
                  <div>
                    <label for="form-provider" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Provider</label>
                    <select
                      id="form-provider"
                      bind:value={formProvider}
                      class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text"
                    >
                      <option value="">Select a provider...</option>
                      {#each providerGroups as group}
                        <optgroup label={group.label}>
                          {#each group.providers as p}
                            <option value={p.key}>{p.display_key || p.key} ({p.config.type})</option>
                          {/each}
                        </optgroup>
                      {/each}
                    </select>
                  </div>
                  <div>
                    <label for="form-model" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Model</label>
                    {#if availableModels.length > 0}
                      <select
                        id="form-model"
                        bind:value={formModel}
                        class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text"
                      >
                        <option value="">Default ({selectedProviderConfig?.config.model})</option>
                        {#each availableModels as m}
                          <option value={m}>{m}</option>
                        {/each}
                      </select>
                    {:else}
                      <input
                        id="form-model"
                        type="text"
                        bind:value={formModel}
                        placeholder="Override default model"
                        class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
                      />
                    {/if}
                  </div>
                </div>
              </div>
            </div>

            <div>
              <label for="form-reasoning-effort" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Reasoning effort</label>
              <select
                id="form-reasoning-effort"
                bind:value={formReasoningEffort}
                aria-describedby="form-reasoning-help form-reasoning-warning"
                aria-invalid={!!reasoningEffortError}
                class="w-full sm:w-64 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text"
              >
                <option value="">Default</option>
                {#each reasoningEffortOptions as effort}
                  <option value={effort}>{effort}</option>
                {/each}
                {#if unlistedReasoningEffort}
                  <option value={formReasoningEffort}>{formReasoningEffort} (current, {reasoningEffortError ? 'unsupported' : 'unverified'})</option>
                {/if}
              </select>
              <p id="form-reasoning-help" class="mt-1 text-xs text-gray-600 dark:text-dark-text-secondary">
                Default uses provider/model defaults; it does not disable thinking.
                {#if forwardsReasoningEffort}
                  This adapter forwards the effort. The actual model and endpoint must support the chosen setting, especially xhigh; not all OpenAI-compatible endpoints do.
                {:else if mapsThinkingBudget}
                  This adapter maps low, medium and high to thinking budgets. The actual model and endpoint must support thinking with the mapped budget.
                {:else if reasoningUnsupported}
                  This adapter does not support reasoning effort overrides. Use Default.
                {:else}
                  Adapter support is unknown. Only Default is offered; existing values are retained but support cannot be verified.
                {/if}
              </p>
              <p id="form-reasoning-warning" aria-live="polite" class="mt-1 text-xs text-amber-700 dark:text-amber-400">
                {#if reasoningEffortError}
                  {reasoningEffortError} Your selection has been retained.
                {:else if unlistedReasoningEffort}
                  The current effort is retained. Verify adapter, model and endpoint support before saving, or choose Default.
                {/if}
              </p>
            </div>

            <!-- Separator -->
            <div class="border-t border-gray-200 dark:border-dark-border"></div>

            <!-- System Prompt -->
            <div>
              <label for="form-system-prompt" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">System Prompt</label>
              <textarea
                id="form-system-prompt"
                bind:value={formSystemPrompt}
                rows={3}
                placeholder="You are a helpful assistant..."
                class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle resize-y dark:text-dark-text dark:placeholder:text-dark-text-muted"
              ></textarea>
            </div>

            <!-- Skills -->
            <div>
              <span class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Skills</span>
              <div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-2 bg-gray-50/50 dark:bg-dark-base/30 p-3 border border-gray-200 dark:border-dark-border">
                {#each skills as skill}
                  <label class="flex items-center gap-2 cursor-pointer">
                    <input type="checkbox" bind:group={formSkills} value={skill.name} class="text-gray-900 dark:text-accent focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:border-dark-border-subtle" />
                    <span class="text-xs text-gray-700 dark:text-dark-text-secondary truncate" title={skill.name}>{skill.name}</span>
                  </label>
                {/each}
                {#if skills.length === 0}
                  <div class="col-span-full text-xs text-gray-400 dark:text-dark-text-muted italic text-center">No skills available</div>
                {/if}
              </div>
            </div>

            <!-- Connections (agent-level, per provider) -->
            {#if connections.length > 0}
              {@const providersWithConnections = Array.from(new Set(connections.map((c) => c.provider))).sort()}
              <div>
                <span class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">
                  Connections
                  <span class="text-gray-400 dark:text-dark-text-muted font-normal ml-1">
                    — which account this agent uses for each provider
                  </span>
                </span>
                <div class="space-y-2 bg-gray-50/50 dark:bg-dark-base/30 p-3 border border-gray-200 dark:border-dark-border">
                  {#each providersWithConnections as provider (provider)}
                    {@const options = connections.filter((c) => c.provider === provider)}
                    <div class="flex items-center gap-2">
                      <label for="form-conn-{provider}" class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary w-24 shrink-0 capitalize">
                        {provider}
                      </label>
                      <select
                        id="form-conn-{provider}"
                        value={formConnections[provider] ?? ''}
                        onchange={(e) => {
                          const v = (e.target as HTMLSelectElement).value;
                          if (v) {
                            formConnections = { ...formConnections, [provider]: v };
                          } else {
                            const next = { ...formConnections };
                            delete next[provider];
                            formConnections = next;
                          }
                        }}
                        class="flex-1 text-xs border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2 py-1 focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text"
                      >
                        <option value="">(fall back to global variables)</option>
                        {#each options as opt (opt.id)}
                          <option value={opt.id}>
                            {opt.name}{opt.account_label ? ` — ${opt.account_label}` : ''}
                          </option>
                        {/each}
                      </select>
                    </div>
                  {/each}
                </div>
              </div>
            {/if}

            <!-- MCP Servers -->
            <div>
              <span class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">MCP Servers</span>
              <div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-2 bg-gray-50/50 dark:bg-dark-base/30 p-3 border border-gray-200 dark:border-dark-border">
                {#each mcpSets as server}
                  <label class="flex items-center gap-2 cursor-pointer">
                    <input type="checkbox" bind:group={formMCPSets} value={server.name} class="text-gray-900 dark:text-accent focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:border-dark-border-subtle" />
                    <span class="text-xs text-gray-700 dark:text-dark-text-secondary truncate" title={server.description || server.name}>{server.name}</span>
                  </label>
                {/each}
                {#if mcpSets.length === 0}
                  <div class="col-span-full text-xs text-gray-400 dark:text-dark-text-muted italic text-center">No MCP servers available</div>
                {/if}
              </div>
            </div>

            <!-- Workflows -->
            <div>
              <span class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">
                <span class="inline-flex items-center gap-1.5">
                  <WorkflowIcon size={12} />
                  Workflows
                </span>
                <span class="text-[10px] text-gray-400 dark:text-dark-text-muted font-normal ml-2">
                  Exposed to the agent as <code class="font-mono">wf_&lt;name&gt;</code> tools
                </span>
              </span>
              <div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-2 bg-gray-50/50 dark:bg-dark-base/30 p-3 border border-gray-200 dark:border-dark-border">
                {#each workflows as wf}
                  <label class="flex items-center gap-2 cursor-pointer" title={wf.description || wf.name}>
                    <input type="checkbox" bind:group={formWorkflows} value={wf.name} class="text-gray-900 dark:text-accent focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:border-dark-border-subtle" />
                    <span class="text-xs text-gray-700 dark:text-dark-text-secondary truncate">{wf.name}</span>
                  </label>
                {/each}
                {#if workflows.length === 0}
                  <div class="col-span-full text-xs text-gray-400 dark:text-dark-text-muted italic text-center">No workflows available</div>
                {/if}
              </div>
            </div>

            <!-- Subagents -->

            <div>
              <span class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">
                Subagents
                <span class="text-[10px] text-gray-400 dark:text-dark-text-muted font-normal ml-2">Isolated workers available through <code class="font-mono">agent_run</code></span>
              </span>
              <div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-2 bg-gray-50/50 dark:bg-dark-base/30 p-3 border border-gray-200 dark:border-dark-border">
                {#each agents.filter((candidate) => candidate.id !== editingId) as candidate}
                  <label class="flex items-center gap-2 cursor-pointer">
                    <input type="checkbox" bind:group={formSubagents} value={candidate.id} class="text-gray-900 dark:text-accent focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:border-dark-border-subtle" />
                    <span class="text-xs text-gray-700 dark:text-dark-text-secondary truncate" title={candidate.config.description || candidate.name}>{candidate.name}</span>
                  </label>
                {/each}
                {#if agents.filter((candidate) => candidate.id !== editingId).length === 0}
                  <div class="col-span-full text-xs text-gray-400 dark:text-dark-text-muted italic text-center">No other agents available</div>
                {/if}
              </div>
            </div>

            <!-- Builtin Tools -->
            <div>
              <span class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">
                <span class="inline-flex items-center gap-1.5">
                  <Wrench size={12} />
                  Builtin Tools
                </span>
              </span>
              <BuiltinToolPicker tools={builtinToolDefs} bind:selected={formBuiltinTools} />
            </div>

            <!-- Confirmation Required Tools -->
            {#if formBuiltinTools.length > 0}
              <div>
                <span class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">
                  <span class="inline-flex items-center gap-1.5">
                    <ShieldCheck size={12} />
                    Confirm Before Run
                  </span>
                  <span class="text-[10px] text-gray-400 dark:text-dark-text-muted font-normal ml-2">Tools requiring human approval</span>
                </span>
                <div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-2 bg-orange-50/50 dark:bg-orange-950/10 p-3 border border-orange-200 dark:border-orange-900/30">
                  {#each formBuiltinTools as toolName}
                    <label class="flex items-center gap-2 cursor-pointer">
                      <input type="checkbox" bind:group={formConfirmationTools} value={toolName} class="text-orange-600 dark:text-orange-400 focus:ring-orange-500/20 dark:bg-dark-elevated dark:border-dark-border-subtle" />
                      <span class="text-xs text-gray-700 dark:text-dark-text-secondary truncate">{toolName}</span>
                    </label>
                  {/each}
                </div>
              </div>
            {/if}

            <!-- MCP URLs (legacy) -->
            {#if formMCPs.some(u => u.trim() !== '')}
              <div>
                <span class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">MCP URLs</span>
                <div class="space-y-2">
                  {#each formMCPs as url, i}
                    <div class="flex gap-2 items-center">
                      <input
                        type="text"
                        value={url}
                        oninput={(e) => updateMcpInput(i, (e.target as HTMLInputElement).value)}
                        class="flex-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text dark:placeholder:text-dark-text-muted"
                        placeholder="https://mcp-server.example.com/mcp"
                      />
                      <button
                        type="button"
                        onclick={() => removeMcpInput(i)}
                        class="p-1 text-red-500 hover:bg-red-50 hover:text-red-700 dark:text-red-400 dark:hover:bg-red-900/20 dark:hover:text-red-300"
                        title="Remove URL"
                      >
                        <X size={14} />
                      </button>
                    </div>
                  {/each}
                  <button
                    type="button"
                    onclick={addMcpInput}
                    class="flex items-center gap-1.5 text-sm text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text "
                  >
                    <Plus size={12} />
                    Add URL
                  </button>
                </div>
              </div>
            {/if}

            {#if isNativeAdmin()}
            <!-- Agent Budget -->
            <div class="border border-gray-200 dark:border-dark-border bg-gray-50/60 dark:bg-dark-base p-3 space-y-3">
              <div class="flex items-center justify-between">
                <div>
                  <span class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary">Spending Budget</span>
                  <span class="text-[10px] text-gray-400 dark:text-dark-text-muted">Checked before each agent LLM call. Blank or 0 disables the limit.</span>
                </div>
                {#if formAgentBudget}
                  <div class="text-right">
                    <span class="block text-xs font-mono text-gray-700 dark:text-dark-text-secondary">
                      ${formAgentBudget.current_spend.toFixed(2)} / {formAgentBudget.monthly_limit > 0 ? `$${formAgentBudget.monthly_limit.toFixed(2)}` : 'Unlimited'}
                    </span>
                    <span class="text-[9px] text-gray-400 dark:text-dark-text-muted">
                      Resets {new Date(formAgentBudget.period_end).toLocaleString(undefined, { timeZone: formAgentBudget.budget_timezone || 'UTC' })}
                    </span>
                  </div>
                {/if}
              </div>

              <label class="block w-56">
                <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Period limit (USD)</span>
                <input
                  type="number"
                  min="0"
                  step="0.01"
                  bind:value={formBudgetLimit}
                  placeholder="Unlimited"
                  class="w-full px-2 py-1 text-xs font-mono border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text"
                />
              </label>

              <BudgetScheduleFields
                bind:period={formBudgetPeriod}
                bind:resetDay={formBudgetResetDay}
                bind:resetTime={formBudgetResetTime}
                bind:timezone={formBudgetTimezone}
              />
            </div>
            {/if}

            <!-- Max Iterations / Tool Timeout -->
            <div class="grid grid-cols-2 gap-3">
              <div>
                <label for="form-max-iterations" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Max Iterations</label>
                <input
                  id="form-max-iterations"
                  type="number"
                  bind:value={formMaxIterations}
                  min="1"
                  class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text"
                />
              </div>
              <div>
                <label for="form-tool-timeout" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Tool Timeout (s)</label>
                <input
                  id="form-tool-timeout"
                  type="number"
                  bind:value={formToolTimeout}
                  min="1"
                  class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:text-dark-text"
                />
              </div>
            </div>

            <!-- Actions -->
            <div class="flex justify-end gap-2 pt-3 border-t border-gray-100 dark:border-dark-border">
              <button
                type="button"
                onclick={resetForm}
                class="px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary "
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={saving || builderBusy}
                class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50"
              >
                <Save size={14} />
                {#if saving}
                  Saving...
                {:else}
                  {editingId ? 'Update' : 'Create'}
                {/if}
              </button>
            </div>
          </form>
        </div>
      {/if}

      <!-- Agent list -->
      {#if loading || agents.length > 0 || !showForm}
        <DataTable
          error={page.list.error('Agents')}
          onretry={loadData}
          items={pagedAgents}
          {loading}
          total={filteredTotal}
          bind:limit
          bind:offset
          onsearch={handleSearch}
          searchPlaceholder="Search name, group, provider..."
          emptyIcon={Bot}
          emptyTitle="No agents configured"
          emptyDescription="Agents combine LLM providers with skills for autonomous workflows"
        >
          {#snippet header()}
            <SortableHeader field="name" label="Name" {sorts} onsort={handleSort} />
            <SortableHeader field="group" label="Group" {sorts} onsort={handleSort} />
            <SortableHeader field="provider" label="Provider / Model" {sorts} onsort={handleSort} />
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-32"></th>
          {/snippet}

          {#snippet row(agent)}
            <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 ">
              <td class="px-4 py-2.5">
                <div class="flex items-center gap-2.5">
                  <img src={agentAvatar(agent.config.avatar_seed, agent.name, 32)} alt="" class="w-8 h-8 rounded-full shrink-0 bg-gray-100 dark:bg-dark-elevated" />
                  <div class="flex flex-col gap-0.5 min-w-0">
                    <div class="flex items-center gap-1.5">
                      <span class="font-mono font-medium text-gray-900 dark:text-dark-text">{agent.name}</span>
                      {#if agent.scope === 'personal'}
                        <span class="px-1.5 py-0 text-[10px] font-medium bg-blue-50 dark:bg-blue-900/20 text-blue-700 dark:text-blue-400 border border-blue-200 dark:border-blue-900/40" title="Personal agent — only visible to its owner">personal</span>
                      {:else if agent.scope === 'global'}
                        <span class="px-1.5 py-0 text-[10px] font-medium bg-purple-50 dark:bg-purple-900/20 text-purple-700 dark:text-purple-400 border border-purple-200 dark:border-purple-900/40" title="Global agent — available in every workspace">global</span>
                      {/if}
                      {#if activeByAgent[agent.id]?.length}
                        <span class="flex items-center gap-1 px-1.5 py-0 text-[10px] font-medium bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-400 border border-green-200 dark:border-green-900/40" title="{activeByAgent[agent.id].length} active task{activeByAgent[agent.id].length === 1 ? '' : 's'}: {activeByAgent[agent.id].map(d => d.duration).join(', ')}">
                          <span class="relative flex w-1.5 h-1.5">
                            <span class="absolute inline-flex w-full h-full rounded-full bg-green-400 opacity-75 animate-ping"></span>
                            <span class="relative inline-flex w-1.5 h-1.5 rounded-full bg-green-500"></span>
                          </span>
                          {activeByAgent[agent.id].length === 1 ? `working · ${activeByAgent[agent.id][0].duration}` : `${activeByAgent[agent.id].length} running`}
                        </span>
                      {/if}
                    </div>
                    {#if agent.config.description}
                      <span class="text-[10px] text-gray-400 dark:text-dark-text-muted truncate max-w-48">{agent.config.description}</span>
                    {/if}
                  </div>
                </div>
              </td>
              <td class="px-4 py-2.5">
                {#if agent.config.group}
                  <span
                    class="inline-flex items-center px-2 py-0.5 text-[10px] font-medium bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary border border-gray-200 dark:border-dark-border truncate max-w-40"
                    title={agent.config.group}
                  >
                    {agent.config.group}
                  </span>
                {:else}
                  <span class="text-xs text-gray-300 dark:text-dark-text-faint">—</span>
                {/if}
              </td>
              <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
                <div class="flex flex-col gap-0.5">
                  <span class="font-mono text-gray-700 dark:text-dark-text-secondary">{agent.config.provider}</span>
                  {#if agent.config.model}
                    <span class="font-mono text-gray-400 dark:text-dark-text-muted text-[10px]">{agent.config.model}</span>
                  {/if}
                </div>
              </td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex justify-end gap-1">
                  {#if agent.owner_user_id === storeAuth.identity?.subject && mayPublish}
                    <button
                      onclick={() => handlePublish(agent)}
                      class="p-1.5 hover:bg-blue-50 dark:hover:bg-accent-muted text-blue-500 hover:text-blue-700 dark:text-accent-text"
                      title="Copy to workspace agents"
                    >
                      <Share2 size={14} />
                    </button>
                  {/if}
                  <button
                    onclick={() => handleExport(agent)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text "
                    title="Export as .md"
                  >
                    <Download size={14} />
                  </button>
                  <button
                    onclick={() => copyAgent(agent)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text "
                    title="Copy agent"
                  >
                    <Copy size={14} />
                  </button>
                  {#if canManageAgent(agent)}
                  <button
                    onclick={() => openEdit(agent)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text "
                    title="Edit"
                  >
                    <Pencil size={14} />
                  </button>
                  {#if deleteConfirm === agent.id}
                    <button
                      onclick={() => handleDelete(agent.id)}
                      class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 "
                    >
                      Confirm
                    </button>
                    <button
                      onclick={() => (deleteConfirm = null)}
                      class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary "
                    >
                      Cancel
                    </button>
                  {:else}
                    <button
                      onclick={() => (deleteConfirm = agent.id)}
                      class="p-1.5 text-red-500 hover:bg-red-50 hover:text-red-700 dark:text-red-400 dark:hover:bg-red-900/20 dark:hover:text-red-300"
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
      {/if}
    </div>
  </div>
  {#if showAIBuilder}
    {#key formVersion}<AgentBuilderPanel getDraft={getAgentDraft} getCatalog={getBuilderCatalog} applyPatch={applyBuilderPatch} contextLoading={page.editorLoading} bind:busy={builderBusy} onclose={() => { showAIBuilder = false; builderBusy = false; }} />{/key}
  {/if}
</div>
