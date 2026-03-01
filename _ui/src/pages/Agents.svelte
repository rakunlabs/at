<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listAgents, createAgent, updateAgent, deleteAgent, type Agent } from '@/lib/api/agents';
  import { listProviders, type ProviderRecord } from '@/lib/api/providers';
  import { listSkills, type Skill } from '@/lib/api/skills';
  import { Trash2, Plus, X, Search, Pencil, Bot, RefreshCw } from 'lucide-svelte';

  storeNavbar.title = 'Agents';

  let agents = $state<Agent[]>([]);
  let providers = $state<ProviderRecord[]>([]);
  let skills = $state<Skill[]>([]);
  let loading = $state(true);
  let showModal = $state(false);
  let editing = $state<Agent | null>(null);
  let saving = $state(false);
  let searchQuery = $state('');

  // Form fields
  let formName = $state('');
  let formDescription = $state('');
  let formProvider = $state('');
  let formModel = $state('');
  let formSystemPrompt = $state('');
  let formSkills = $state<string[]>([]);
  let formMCPs = $state<string[]>(['']); // One empty string for initial input
  let formMaxIterations = $state(10);
  let formToolTimeout = $state(60);

  // Init
  loadData();

  async function loadData() {
    loading = true;
    try {
      const [a, p, s] = await Promise.all([listAgents(), listProviders(), listSkills()]);
      agents = a;
      providers = p;
      skills = s;
    } catch (e: any) {
      addToast(e?.message || 'Failed to load data', 'alert');
    } finally {
      loading = false;
    }
  }

  function openCreate() {
    editing = null;
    formName = '';
    formDescription = '';
    formProvider = '';
    formModel = '';
    formSystemPrompt = '';
    formSkills = [];
    formMCPs = [''];
    formMaxIterations = 10;
    formToolTimeout = 60;
    showModal = true;
  }

  function openEdit(agent: Agent) {
    editing = agent;
    formName = agent.name;
    formDescription = agent.description;
    formProvider = agent.provider;
    formModel = agent.model;
    formSystemPrompt = agent.system_prompt;
    formSkills = [...agent.skills];
    formMCPs = agent.mcp_urls && agent.mcp_urls.length > 0 ? [...agent.mcp_urls] : [''];
    formMaxIterations = agent.max_iterations || 10;
    formToolTimeout = agent.tool_timeout || 60;
    showModal = true;
  }

  async function handleSave() {
    if (!formName || !formProvider) {
      addToast('Name and Provider are required', 'alert');
      return;
    }

    saving = true;
    try {
      const cleanMCPs = formMCPs.filter(u => u.trim() !== '');
      const data = {
        name: formName,
        description: formDescription,
        provider: formProvider,
        model: formModel,
        system_prompt: formSystemPrompt,
        skills: formSkills,
        mcp_urls: cleanMCPs,
        max_iterations: formMaxIterations,
        tool_timeout: formToolTimeout,
      };

      if (editing) {
        await updateAgent(editing.id, data);
        addToast('Agent updated', 'info');
      } else {
        await createAgent(data);
        addToast('Agent created', 'info');
      }
      showModal = false;
      loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save agent', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    if (!confirm('Are you sure you want to delete this agent?')) return;
    try {
      await deleteAgent(id);
      addToast('Agent deleted', 'info');
      loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete agent', 'alert');
    }
  }

  function addMcpInput() {
    formMCPs = [...formMCPs, ''];
  }

  function removeMcpInput(i: number) {
    formMCPs = formMCPs.filter((_, idx) => idx !== i);
  }

  function updateMcpInput(i: number, val: string) {
    formMCPs[i] = val;
  }

  let selectedProviderConfig = $derived(providers.find(p => p.key === formProvider));
  let availableModels = $derived(
    selectedProviderConfig?.config?.models?.length
      ? selectedProviderConfig.config.models
      : selectedProviderConfig?.config?.model
        ? [selectedProviderConfig.config.model]
        : []
  );

  let filteredAgents = $derived(
    agents.filter(a =>
      a.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
      a.description.toLowerCase().includes(searchQuery.toLowerCase())
    )
  );
</script>

<div class="flex h-full">
  <div class="flex-1 overflow-y-auto">
    <div class="p-6 max-w-5xl mx-auto">
      <div class="flex items-center justify-between mb-4">
        <div class="flex items-center gap-2">
          <Bot size={16} class="text-gray-500 dark:text-dark-text-muted" />
          <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Agents</h2>
          <span class="text-xs text-gray-400 dark:text-dark-text-muted">({agents.length})</span>
        </div>
        <div class="flex items-center gap-2">
          <div class="relative w-48 mr-2">
            <Search class="absolute left-2 top-1.5 text-gray-400 dark:text-dark-text-muted" size={12} />
            <input
              type="text"
              bind:value={searchQuery}
              placeholder="Search..."
              class="w-full pl-7 pr-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated focus:outline-none focus:border-gray-500 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
            />
          </div>
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

      {#if loading}
        <div class="px-4 py-10 text-center text-gray-400 dark:text-dark-text-muted text-sm">Loading...</div>
      {:else if filteredAgents.length === 0}
        <div class="px-4 py-10 text-center">
          <Bot size={24} class="mx-auto text-gray-300 dark:text-dark-text-faint mb-2" />
          <div class="text-gray-400 dark:text-dark-text-muted mb-1">No agents found</div>
        </div>
      {:else}
        <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface shadow-sm overflow-hidden">
          <table class="w-full text-sm">
            <thead>
              <tr class="border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50">
                <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Name</th>
                <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Description</th>
                <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Provider / Model</th>
                <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Skills</th>
                <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-24"></th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-border">
              {#each filteredAgents as agent (agent.id)}
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
                    {:else}
                      <span class="text-gray-400 dark:text-dark-text-muted">none</span>
                    {/if}
                  </td>
                  <td class="px-4 py-2.5 text-right">
                    <div class="flex justify-end gap-1">
                      <button
                        onclick={() => openEdit(agent)}
                        class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
                        title="Edit"
                      >
                        <Pencil size={14} />
                      </button>
                      <button
                        onclick={() => handleDelete(agent.id)}
                        class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 hover:text-red-600 dark:text-dark-text-muted dark:hover:text-red-400 transition-colors"
                        title="Delete"
                      >
                        <Trash2 size={14} />
                      </button>
                    </div>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    </div>
  </div>
</div>

{#if showModal}
  <div class="fixed inset-0 bg-black/50 flex items-center justify-center z-50 p-4">
    <div class="bg-white dark:bg-dark-surface shadow-xl w-full max-w-2xl max-h-[90vh] flex flex-col border border-transparent dark:border-dark-border">
      <div class="flex items-center justify-between px-6 py-4 border-b border-gray-100 dark:border-dark-border">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-dark-text">{editing ? 'Edit Agent' : 'Create Agent'}</h2>
        <button onclick={() => (showModal = false)} class="text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary">
          <X size={20} />
        </button>
      </div>

      <div class="p-6 overflow-y-auto flex-1 space-y-4">
        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Name</label>
            <input
              type="text"
              bind:value={formName}
              class="w-full px-3 py-2 text-sm border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated focus:outline-none focus:ring-1 focus:ring-blue-500 dark:focus:ring-accent/40 dark:text-dark-text dark:placeholder:text-dark-text-muted"
              placeholder="e.g. Code Reviewer"
            />
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Description</label>
            <input
              type="text"
              bind:value={formDescription}
              class="w-full px-3 py-2 text-sm border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated focus:outline-none focus:ring-1 focus:ring-blue-500 dark:focus:ring-accent/40 dark:text-dark-text dark:placeholder:text-dark-text-muted"
              placeholder="Brief description"
            />
          </div>
        </div>

        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Provider</label>
            <select
              bind:value={formProvider}
              class="w-full px-3 py-2 text-sm border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated focus:outline-none focus:ring-1 focus:ring-blue-500 dark:focus:ring-accent/40 dark:text-dark-text"
            >
              <option value="">Select a provider...</option>
              {#each providers as p}
                <option value={p.key}>{p.key} ({p.config.type})</option>
              {/each}
            </select>
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Model (Optional)</label>
            {#if availableModels.length > 0}
              <select
                bind:value={formModel}
                class="w-full px-3 py-2 text-sm border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated focus:outline-none focus:ring-1 focus:ring-blue-500 dark:focus:ring-accent/40 dark:text-dark-text"
              >
                <option value="">Default ({selectedProviderConfig?.config.model})</option>
                {#each availableModels as m}
                  <option value={m}>{m}</option>
                {/each}
              </select>
            {:else}
              <input
                type="text"
                bind:value={formModel}
                class="w-full px-3 py-2 text-sm border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated focus:outline-none focus:ring-1 focus:ring-blue-500 dark:focus:ring-accent/40 dark:text-dark-text dark:placeholder:text-dark-text-muted"
                placeholder="Override default model"
              />
            {/if}
          </div>
        </div>

        <div>
          <label class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">System Prompt</label>
          <textarea
            bind:value={formSystemPrompt}
            rows={4}
            class="w-full px-3 py-2 text-sm border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated focus:outline-none focus:ring-1 focus:ring-blue-500 dark:focus:ring-accent/40 font-mono text-xs dark:text-dark-text dark:placeholder:text-dark-text-muted"
            placeholder="You are a helpful assistant..."
          ></textarea>
        </div>

        <div>
          <label class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Skills</label>
          <div class="grid grid-cols-2 sm:grid-cols-3 gap-2 bg-gray-50 dark:bg-dark-base p-3 border border-gray-200 dark:border-dark-border">
            {#each skills as skill}
              <label class="flex items-center gap-2 cursor-pointer">
                <input type="checkbox" bind:group={formSkills} value={skill.name} class="text-blue-600 focus:ring-blue-500 dark:bg-dark-elevated dark:border-dark-border-subtle" />
                <span class="text-xs text-gray-700 dark:text-dark-text-secondary truncate" title={skill.name}>{skill.name}</span>
              </label>
            {/each}
            {#if skills.length === 0}
              <div class="col-span-full text-xs text-gray-400 dark:text-dark-text-muted italic text-center">No skills available</div>
            {/if}
          </div>
        </div>

        <div>
          <div class="flex items-center justify-between mb-1">
            <label class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary">MCP Servers</label>
            <button onclick={addMcpInput} class="text-xs text-blue-600 dark:text-accent-text hover:text-blue-800 dark:hover:text-accent flex items-center gap-1">
              <Plus size={12} /> Add URL
            </button>
          </div>
          <div class="space-y-2">
            {#each formMCPs as url, i}
              <div class="flex gap-2">
                <input
                  type="text"
                  value={url}
                  oninput={(e) => updateMcpInput(i, e.currentTarget.value)}
                  class="flex-1 px-3 py-2 text-sm border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated focus:outline-none focus:ring-1 focus:ring-blue-500 dark:focus:ring-accent/40 font-mono text-xs dark:text-dark-text dark:placeholder:text-dark-text-muted"
                  placeholder="http://localhost:8000/sse"
                />
                <button onclick={() => removeMcpInput(i)} class="text-gray-400 hover:text-red-600 dark:text-dark-text-muted dark:hover:text-red-400 px-1">
                  <Trash2 size={16} />
                </button>
              </div>
            {/each}
          </div>
        </div>

        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Max Iterations</label>
            <input
              type="number"
              bind:value={formMaxIterations}
              class="w-full px-3 py-2 text-sm border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated focus:outline-none focus:ring-1 focus:ring-blue-500 dark:focus:ring-accent/40 dark:text-dark-text"
              min="1"
            />
          </div>
          <div>
            <label class="block text-xs font-medium text-gray-700 dark:text-dark-text-secondary mb-1">Tool Timeout (seconds)</label>
            <input
              type="number"
              bind:value={formToolTimeout}
              class="w-full px-3 py-2 text-sm border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated focus:outline-none focus:ring-1 focus:ring-blue-500 dark:focus:ring-accent/40 dark:text-dark-text"
              min="1"
            />
          </div>
        </div>
      </div>

      <div class="px-6 py-4 bg-gray-50 dark:bg-dark-base border-t border-gray-100 dark:border-dark-border flex justify-end gap-3">
        <button
          onclick={() => (showModal = false)}
          class="px-4 py-2 text-sm font-medium text-gray-700 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text"
        >
          Cancel
        </button>
        <button
          onclick={handleSave}
          disabled={saving}
          class="px-4 py-2 text-sm font-medium text-white bg-gray-900 dark:bg-accent hover:bg-gray-800 dark:hover:bg-accent-hover disabled:opacity-50"
        >
          {saving ? 'Saving...' : editing ? 'Update Agent' : 'Create Agent'}
        </button>
      </div>
    </div>
  </div>
{/if}