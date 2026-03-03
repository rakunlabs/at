<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listAgents, createAgent, updateAgent, deleteAgent, type Agent } from '@/lib/api/agents';
  import { listProviders, type ProviderRecord } from '@/lib/api/providers';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import { Trash2, Plus, X, Search, Pencil, Bot, RefreshCw, Save, Copy, ClipboardPaste } from 'lucide-svelte';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';

  storeNavbar.title = 'Agents';

  // ─── State ───

  let agents = $state<Agent[]>([]);
  let providers = $state<ProviderRecord[]>([]);
  let skills = $state<Skill[]>([]);
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
  let formMCPs = $state<string[]>(['']);
  let formMaxIterations = $state(10);
  let formToolTimeout = $state(60);

  // Copy / Paste via system clipboard
  
  async function copyAgent(agent: Agent) {
    const exportData = {
      name: agent.name,
      description: agent.description,
      provider: agent.provider,
      model: agent.model,
      system_prompt: agent.system_prompt,
      skills: agent.skills || [],
      mcp_urls: agent.mcp_urls || [],
      max_iterations: agent.max_iterations,
      tool_timeout: agent.tool_timeout,
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
      formDescription = src.description || '';
      formProvider = src.provider || '';
      formModel = src.model || '';
      formSystemPrompt = src.system_prompt || '';
      formSkills = src.skills || [];
      formMCPs = src.mcp_urls && src.mcp_urls.length > 0 ? [...src.mcp_urls] : [''];
      formMaxIterations = src.max_iterations || 10;
      formToolTimeout = src.tool_timeout || 60;
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
      
      const [aResult, pResult, sResult] = await Promise.all([listAgents(params), listProviders(), listSkills()]);
      agents = aResult.data || [];
      total = aResult.meta?.total || 0;
      providers = pResult.data || [];
      skills = sResult.data || [];
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
    formMCPs = [''];
    formMaxIterations = 10;
    formToolTimeout = 60;
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
    formDescription = agent.description;
    formProvider = agent.provider;
    formModel = agent.model;
    formSystemPrompt = agent.system_prompt;
    formSkills = [...agent.skills];
    formMCPs = agent.mcp_urls && agent.mcp_urls.length > 0 ? [...agent.mcp_urls] : [''];
    formMaxIterations = agent.max_iterations || 10;
    formToolTimeout = agent.tool_timeout || 60;
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
        description: formDescription.trim(),
        provider: formProvider,
        model: formModel,
        system_prompt: formSystemPrompt,
        skills: formSkills,
        mcp_urls: cleanMCPs,
        max_iterations: formMaxIterations,
        tool_timeout: formToolTimeout,
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
            <!-- Name -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-name" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Name</label>
              <input
                id="form-name"
                type="text"
                bind:value={formName}
                placeholder="e.g., code_reviewer, data_analyst"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>

            <!-- Description -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-description" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Description</label>
              <input
                id="form-description"
                type="text"
                bind:value={formDescription}
                placeholder="What this agent does"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>

            <!-- Provider -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-provider" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Provider</label>
              <select
                id="form-provider"
                bind:value={formProvider}
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text"
              >
                <option value="">Select a provider...</option>
                {#each providers as p}
                  <option value={p.key}>{p.key} ({p.config.type})</option>
                {/each}
              </select>
            </div>

            <!-- Model -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-model" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Model</label>
              {#if availableModels.length > 0}
                <select
                  id="form-model"
                  bind:value={formModel}
                  class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text"
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
                  class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
                />
              {/if}
            </div>

            <!-- System Prompt -->
            <div class="grid grid-cols-4 gap-3 items-start">
              <label for="form-system-prompt" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">System Prompt</label>
              <textarea
                id="form-system-prompt"
                bind:value={formSystemPrompt}
                rows={3}
                placeholder="You are a helpful assistant..."
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle resize-y transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
              ></textarea>
            </div>

            <!-- Skills -->
            <div class="grid grid-cols-4 gap-3 items-start">
              <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Skills</span>
              <div class="col-span-3 grid grid-cols-2 sm:grid-cols-3 gap-2 bg-gray-50/50 dark:bg-dark-base/30 p-3 border border-gray-200 dark:border-dark-border">
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
            <div class="grid grid-cols-4 gap-3 items-start">
              <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">MCP Servers</span>
              <div class="col-span-3 space-y-2">
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

            <!-- Max Iterations / Tool Timeout -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-max-iterations" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Max Iterations</label>
              <input
                id="form-max-iterations"
                type="number"
                bind:value={formMaxIterations}
                min="1"
                class="col-span-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text"
              />
              <label for="form-tool-timeout" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary text-right">Tool Timeout (s)</label>
              <input
                id="form-tool-timeout"
                type="number"
                bind:value={formToolTimeout}
                min="1"
                class="col-span-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text"
              />
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
          {#snippet emptyAction()}
            <button
              onclick={openCreate}
              class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors mx-auto"
            >
              <Plus size={12} />
              New Agent
            </button>
          {/snippet}

          {#snippet header()}
            <SortableHeader field="name" label="Name" {sorts} onsort={handleSort} />
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Description</th>
            <SortableHeader field="provider" label="Provider / Model" {sorts} onsort={handleSort} />
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Skills</th>
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-32"></th>
          {/snippet}

          {#snippet row(agent)}
            <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors">
              <td class="px-4 py-2.5 font-mono font-medium text-gray-900 dark:text-dark-text">{agent.name}</td>
              <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted max-w-64 truncate" title={agent.description}>
                {agent.description || '-'}
              </td>
              <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
                <div class="flex flex-col gap-0.5">
                  <span class="font-mono text-gray-700 dark:text-dark-text-secondary">{agent.provider}</span>
                  {#if agent.model}
                    <span class="font-mono text-gray-400 dark:text-dark-text-muted text-[10px]">{agent.model}</span>
                  {/if}
                </div>
              </td>
              <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
                {#if agent.skills && agent.skills.length > 0}
                  <span class="px-2 py-0.5 bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary font-mono">
                    {agent.skills.length} skill{agent.skills.length !== 1 ? 's' : ''}
                  </span>
                  <span class="ml-1.5 text-gray-400 dark:text-dark-text-muted">
                    {agent.skills.join(', ')}
                  </span>
                {:else}
                  <span class="text-gray-400 dark:text-dark-text-muted">none</span>
                {/if}
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
