<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listAgents, createAgent, updateAgent, deleteAgent, type Agent } from '@/lib/api/agents';
  import { listProviders, type ProviderRecord } from '@/lib/api/providers';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import { listMCPServers, type MCPServer } from '@/lib/api/mcp-servers';
  import { listBuiltinTools, type BuiltinToolDef } from '@/lib/api/mcp';
  import { Trash2, Plus, X, Pencil, Bot, RefreshCw, RefreshCcw, Save, Copy, ClipboardPaste, Wrench, ShieldCheck } from 'lucide-svelte';
  import { agentAvatar, generateAvatar } from '@/lib/helper/avatar';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';

  storeNavbar.title = 'Agents';

  // ─── State ───

  let agents = $state<Agent[]>([]);
  let providers = $state<ProviderRecord[]>([]);
  let skills = $state<Skill[]>([]);
  let mcpServers = $state<MCPServer[]>([]);
  let builtinToolDefs = $state<BuiltinToolDef[]>([]);
  let loading = $state(true);
  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let saving = $state(false);
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);
  
  // Pagination
  let offset = $state(0);
  let limit = $state(10);
  let total = $state(0);

  // Form fields
  let formName = $state('');
  let formDescription = $state('');
  let formProvider = $state('');
  let formModel = $state('');
  let formSystemPrompt = $state('');
  let formSkills = $state<string[]>([]);
  let formMCPServers = $state<string[]>([]);
  let formBuiltinTools = $state<string[]>([]);
  let formMCPs = $state<string[]>(['']);
  let formMaxIterations = $state(10);
  let formToolTimeout = $state(60);
  let formConfirmationTools = $state<string[]>([]);
  let formAvatarSeed = $state('');
  let showAvatarSeed = $state(false);

  // Copy / Paste via system clipboard
  
  async function copyAgent(agent: Agent) {
    const exportData = {
      name: agent.name,
      config: {
        description: agent.config.description,
        provider: agent.config.provider,
        model: agent.config.model,
        system_prompt: agent.config.system_prompt,
        skills: agent.config.skills || [],
        mcp_servers: agent.config.mcp_servers || [],
        builtin_tools: agent.config.builtin_tools || [],
        mcp_urls: agent.config.mcp_urls || [],
        max_iterations: agent.config.max_iterations,
        tool_timeout: agent.config.tool_timeout,
        confirmation_required_tools: agent.config.confirmation_required_tools || [],
        avatar_seed: agent.config.avatar_seed || '',
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
      formProvider = cfg.provider || '';
      formModel = cfg.model || '';
      formSystemPrompt = cfg.system_prompt || '';
      formSkills = cfg.skills || [];
      formMCPServers = cfg.mcp_servers || [];
      formBuiltinTools = cfg.builtin_tools || [];
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

  // ─── Load ───

  async function loadData() {
    loading = true;
    try {
      const params: any = { _offset: offset, _limit: limit };
      if (searchQuery) {
        params['name[like]'] = `%${searchQuery}%`;
      }
      const sortParam = buildSortParam(sorts);
      if (sortParam) params._sort = sortParam;
      
      const [aResult, pResult, sResult, mResult, btResult] = await Promise.all([listAgents(params), listProviders(), listSkills(), listMCPServers({ _limit: 500 }), listBuiltinTools()]);
      agents = aResult.data || [];
      total = aResult.meta?.total || 0;
      providers = pResult.data || [];
      skills = sResult.data || [];
      mcpServers = mResult.data || [];
      builtinToolDefs = btResult.tools || [];
    } catch (e: any) {
      addToast(e?.message || 'Failed to load data', 'alert');
    } finally {
      loading = false;
    }
  }

  function handleSearch(value: string) {
    searchQuery = value;
    offset = 0;
    loadData();
  }

  function handleSort(field: string, multiSort: boolean) {
    sorts = toggleSort(sorts, field, multiSort);
    offset = 0;
    loadData();
  }

  loadData();

  // ─── Form ───

  function resetForm() {
    formName = '';
    formDescription = '';
    formProvider = '';
    formModel = '';
    formSystemPrompt = '';
    formSkills = [];
    formMCPServers = [];
    formBuiltinTools = [];
    formMCPs = [''];
    formMaxIterations = 10;
    formToolTimeout = 60;
    formConfirmationTools = [];
    formAvatarSeed = '';
    showAvatarSeed = false;
    editingId = null;
    showForm = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(agent: Agent) {
    resetForm();
    editingId = agent.id;
    formName = agent.name;
    formDescription = agent.config.description;
    formProvider = agent.config.provider;
    formModel = agent.config.model;
    formSystemPrompt = agent.config.system_prompt;
    formSkills = [...(agent.config.skills || [])];
    formMCPServers = [...(agent.config.mcp_servers || [])];
    formBuiltinTools = [...(agent.config.builtin_tools || [])];
    formMCPs = agent.config.mcp_urls && agent.config.mcp_urls.length > 0 ? [...agent.config.mcp_urls] : [''];
    formMaxIterations = agent.config.max_iterations || 10;
    formToolTimeout = agent.config.tool_timeout || 60;
    formConfirmationTools = [...(agent.config.confirmation_required_tools || [])];
    formAvatarSeed = agent.config.avatar_seed || '';
    showForm = true;
  }

  async function handleSubmit() {
    if (!formName.trim()) {
      addToast('Agent name is required', 'warn');
      return;
    }
    if (!formProvider) {
      addToast('Provider is required', 'warn');
      return;
    }

    saving = true;
    try {
      const cleanMCPs = formMCPs.filter(u => u.trim() !== '');
      const payload = {
        name: formName.trim(),
        config: {
          description: formDescription.trim(),
          provider: formProvider,
          model: formModel,
          system_prompt: formSystemPrompt,
          skills: formSkills,
          mcp_servers: formMCPServers,
          builtin_tools: formBuiltinTools,
          mcp_urls: cleanMCPs,
          max_iterations: formMaxIterations,
          tool_timeout: formToolTimeout,
          confirmation_required_tools: formConfirmationTools,
          avatar_seed: formAvatarSeed || undefined,
        },
      };

      if (editingId) {
        await updateAgent(editingId, payload);
        addToast(`Agent "${formName}" updated`);
      } else {
        await createAgent(payload);
        addToast(`Agent "${formName}" created`);
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

<div class="flex h-full">
  <div class="flex-1 overflow-y-auto">
    <div class="p-6 max-w-5xl mx-auto">
      <!-- Header -->
      <div class="flex items-center justify-between mb-4">
        <div class="flex items-center gap-2">
          <Bot size={16} class="text-gray-500 dark:text-dark-text-muted" />
          <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Agents</h2>
          <span class="text-xs text-gray-400 dark:text-dark-text-muted">({total})</span>
        </div>
        <div class="flex items-center gap-2">
          <button
            onclick={loadData}
            class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors"
            title="Refresh"
          >
            <RefreshCw size={14} />
          </button>
          <button
            onclick={openCreate}
            class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors"
          >
            <Plus size={12} />
            New Agent
          </button>
        </div>
      </div>

      <!-- Inline Form -->
      {#if showForm}
        <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
          <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50">
            <div class="flex items-center gap-2">
              <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
                {editingId ? `Edit: ${formName}` : 'New Agent'}
              </span>
              {#if !editingId}
                <button
                  type="button"
                  onclick={pasteAgent}
                  class="flex items-center gap-1 px-2 py-1 text-xs font-medium border border-gray-300 dark:border-dark-border-subtle text-gray-600 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated hover:text-gray-900 dark:hover:text-dark-text transition-colors"
                  title="Paste agent from clipboard"
                >
                  <ClipboardPaste size={12} />
                  Paste
                </button>
              {/if}
            </div>
            <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors">
              <X size={14} />
            </button>
          </div>

          <form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
            <!-- Profile Header: Avatar left, identity fields right -->
            <div class="flex gap-6 items-start">
              <!-- Avatar (large, left side) -->
              <div class="group relative shrink-0 w-[200px] h-[200px]">
                <img
                  src={generateAvatar(formAvatarSeed || formName || 'agent', 200)}
                  alt="Agent avatar"
                  class="w-[200px] h-[200px] bg-gray-100 dark:bg-dark-elevated border border-gray-200 dark:border-dark-border"
                />
                <!-- Overlay buttons — visible on hover -->
                <div class="absolute top-1.5 right-1.5 flex gap-1 opacity-0 group-hover:opacity-100 transition-opacity">
                  <button
                    type="button"
                    onclick={() => { showAvatarSeed = !showAvatarSeed; }}
                    class="p-1 bg-black/30 hover:bg-black/50 text-white/70 hover:text-white transition-colors"
                    title="Custom seed"
                  >
                    <Pencil size={13} />
                  </button>
                  <button
                    type="button"
                    onclick={() => { formAvatarSeed = (formName || 'agent') + '_' + Math.random().toString(36).slice(2, 8); }}
                    class="p-1 bg-black/30 hover:bg-black/50 text-white/70 hover:text-white transition-colors"
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
              <div class="flex-1 space-y-3">
                <!-- Name -->
                <div>
                  <label for="form-name" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Name</label>
                  <input
                    id="form-name"
                    type="text"
                    bind:value={formName}
                    placeholder="e.g., code_reviewer, data_analyst"
                    class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
                  />
                </div>

                <!-- Description -->
                <div>
                  <label for="form-description" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Description</label>
                  <input
                    id="form-description"
                    type="text"
                    bind:value={formDescription}
                    placeholder="What this agent does"
                    class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
                  />
                </div>

                <!-- Provider + Model (side by side) -->
                <div class="grid grid-cols-2 gap-3">
                  <div>
                    <label for="form-provider" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Provider</label>
                    <select
                      id="form-provider"
                      bind:value={formProvider}
                      class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text"
                    >
                      <option value="">Select a provider...</option>
                      {#each providers as p}
                        <option value={p.key}>{p.key} ({p.config.type})</option>
                      {/each}
                    </select>
                  </div>
                  <div>
                    <label for="form-model" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Model</label>
                    {#if availableModels.length > 0}
                      <select
                        id="form-model"
                        bind:value={formModel}
                        class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text"
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
                        class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
                      />
                    {/if}
                  </div>
                </div>
              </div>
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
                class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle resize-y transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
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

            <!-- MCP Servers -->
            <div>
              <span class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">MCP Servers</span>
              <div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-2 bg-gray-50/50 dark:bg-dark-base/30 p-3 border border-gray-200 dark:border-dark-border">
                {#each mcpServers as server}
                  <label class="flex items-center gap-2 cursor-pointer">
                    <input type="checkbox" bind:group={formMCPServers} value={server.name} class="text-gray-900 dark:text-accent focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:border-dark-border-subtle" />
                    <span class="text-xs text-gray-700 dark:text-dark-text-secondary truncate" title={server.description || server.name}>{server.name}</span>
                  </label>
                {/each}
                {#if mcpServers.length === 0}
                  <div class="col-span-full text-xs text-gray-400 dark:text-dark-text-muted italic text-center">No MCP servers available</div>
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
              <div class="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-2 bg-gray-50/50 dark:bg-dark-base/30 p-3 border border-gray-200 dark:border-dark-border">
                {#each builtinToolDefs as tool}
                  <label class="flex items-center gap-2 cursor-pointer" title={tool.description}>
                    <input type="checkbox" bind:group={formBuiltinTools} value={tool.name} class="text-gray-900 dark:text-accent focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:border-dark-border-subtle" />
                    <span class="text-xs text-gray-700 dark:text-dark-text-secondary truncate">{tool.name}</span>
                  </label>
                {/each}
                {#if builtinToolDefs.length === 0}
                  <div class="col-span-full text-xs text-gray-400 dark:text-dark-text-muted italic text-center">No builtin tools available</div>
                {/if}
              </div>
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
                        class="flex-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
                        placeholder="http://localhost:8000/sse"
                      />
                      <button
                        type="button"
                        onclick={() => removeMcpInput(i)}
                        class="p-1 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 hover:text-red-600 dark:text-dark-text-muted dark:hover:text-red-400 transition-colors"
                        title="Remove URL"
                      >
                        <X size={14} />
                      </button>
                    </div>
                  {/each}
                  <button
                    type="button"
                    onclick={addMcpInput}
                    class="flex items-center gap-1.5 text-sm text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
                  >
                    <Plus size={12} />
                    Add URL
                  </button>
                </div>
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
                  class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text"
                />
              </div>
              <div>
                <label for="form-tool-timeout" class="block text-xs font-medium text-gray-500 dark:text-dark-text-muted mb-1">Tool Timeout (s)</label>
                <input
                  id="form-tool-timeout"
                  type="number"
                  bind:value={formToolTimeout}
                  min="1"
                  class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text"
                />
              </div>
            </div>

            <!-- Actions -->
            <div class="flex justify-end gap-2 pt-3 border-t border-gray-100 dark:border-dark-border">
              <button
                type="button"
                onclick={resetForm}
                class="px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary transition-colors"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={saving}
                class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
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
          items={agents}
          {loading}
          {total}
          {limit}
          bind:offset
          onchange={loadData}
          onsearch={handleSearch}
          searchPlaceholder="Search by name..."
          emptyIcon={Bot}
          emptyTitle="No agents configured"
          emptyDescription="Agents combine LLM providers with skills for autonomous workflows"
        >
          {#snippet header()}
            <SortableHeader field="name" label="Name" {sorts} onsort={handleSort} />
            <SortableHeader field="provider" label="Provider / Model" {sorts} onsort={handleSort} />
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-32"></th>
          {/snippet}

          {#snippet row(agent)}
            <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors">
              <td class="px-4 py-2.5">
                <div class="flex items-center gap-2.5">
                  <img src={agentAvatar(agent.config.avatar_seed, agent.name, 32)} alt="" class="w-8 h-8 rounded-full shrink-0 bg-gray-100 dark:bg-dark-elevated" />
                  <div class="flex flex-col gap-0.5 min-w-0">
                    <span class="font-mono font-medium text-gray-900 dark:text-dark-text">{agent.name}</span>
                    {#if agent.config.description}
                      <span class="text-[10px] text-gray-400 dark:text-dark-text-muted truncate max-w-48">{agent.config.description}</span>
                    {/if}
                  </div>
                </div>
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
                  <button
                    onclick={() => copyAgent(agent)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
                    title="Copy agent"
                  >
                    <Copy size={14} />
                  </button>
                  <button
                    onclick={() => openEdit(agent)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
                    title="Edit"
                  >
                    <Pencil size={14} />
                  </button>
                  {#if deleteConfirm === agent.id}
                    <button
                      onclick={() => handleDelete(agent.id)}
                      class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors"
                    >
                      Confirm
                    </button>
                    <button
                      onclick={() => (deleteConfirm = null)}
                      class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary transition-colors"
                    >
                      Cancel
                    </button>
                  {:else}
                    <button
                      onclick={() => (deleteConfirm = agent.id)}
                      class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 hover:text-red-600 dark:text-dark-text-muted dark:hover:text-red-400 transition-colors"
                      title="Delete"
                    >
                      <Trash2 size={14} />
                    </button>
                  {/if}
                </div>
              </td>
            </tr>
          {/snippet}
        </DataTable>
      {/if}
    </div>
  </div>
</div>
