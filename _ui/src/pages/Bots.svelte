<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listBotConfigs, createBotConfig, updateBotConfig, deleteBotConfig, type BotConfig } from '@/lib/api/bots';
  import { listAgents, type Agent } from '@/lib/api/agents';
  import { Trash2, Plus, X, Pencil, Radio, RefreshCw, Save } from 'lucide-svelte';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';

  storeNavbar.title = 'Bots';

  // ─── State ───

  let bots = $state<BotConfig[]>([]);
  let agents = $state<Agent[]>([]);
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
  let formPlatform = $state('discord');
  let formName = $state('');
  let formToken = $state('');
  let formDefaultAgentID = $state('');
  let formEnabled = $state(true);
  let formChannelAgents = $state<{ key: string; value: string }[]>([]);

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

      const [bResult, aResult] = await Promise.all([listBotConfigs(params), listAgents({ _limit: 500 })]);
      bots = bResult.data || [];
      total = bResult.meta?.total || 0;
      agents = aResult.data || [];
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
    formPlatform = 'discord';
    formName = '';
    formToken = '';
    formDefaultAgentID = '';
    formEnabled = true;
    formChannelAgents = [];
    editingId = null;
    showForm = false;
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  function openEdit(bot: BotConfig) {
    resetForm();
    editingId = bot.id;
    formPlatform = bot.platform;
    formName = bot.name;
    formToken = bot.token;
    formDefaultAgentID = bot.default_agent_id;
    formEnabled = bot.enabled;
    formChannelAgents = Object.entries(bot.channel_agents || {}).map(([key, value]) => ({ key, value }));
    showForm = true;
  }

  async function handleSubmit() {
    if (!formPlatform) {
      addToast('Platform is required', 'warn');
      return;
    }
    if (!formToken.trim()) {
      addToast('Bot token is required', 'warn');
      return;
    }

    saving = true;
    try {
      const channelAgents: Record<string, string> = {};
      for (const entry of formChannelAgents) {
        if (entry.key.trim() && entry.value.trim()) {
          channelAgents[entry.key.trim()] = entry.value.trim();
        }
      }

      const payload: Partial<BotConfig> = {
        platform: formPlatform,
        name: formName.trim(),
        token: formToken.trim(),
        default_agent_id: formDefaultAgentID,
        channel_agents: channelAgents,
        enabled: formEnabled,
      };

      if (editingId) {
        await updateBotConfig(editingId, payload);
        addToast(`Bot "${formName || formPlatform}" updated`);
      } else {
        await createBotConfig(payload);
        addToast(`Bot "${formName || formPlatform}" created`);
      }
      resetForm();
      await loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save bot config', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteBotConfig(id);
      addToast('Bot config deleted');
      deleteConfirm = null;
      await loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete bot config', 'alert');
    }
  }

  // ─── Channel agents management ───

  function addChannelAgent() {
    formChannelAgents = [...formChannelAgents, { key: '', value: '' }];
  }

  function removeChannelAgent(i: number) {
    formChannelAgents = formChannelAgents.filter((_, idx) => idx !== i);
  }

  // ─── Helpers ───

  function agentName(id: string): string {
    const a = agents.find(a => a.id === id);
    return a ? a.name : id.slice(0, 8) + '...';
  }

  function maskToken(token: string): string {
    if (token.length <= 8) return '****';
    return token.slice(0, 4) + '...' + token.slice(-4);
  }
</script>

<svelte:head>
  <title>AT | Bots</title>
</svelte:head>

<div class="flex h-full">
  <div class="flex-1 overflow-y-auto">
    <div class="p-6 max-w-5xl mx-auto">
      <!-- Header -->
      <div class="flex items-center justify-between mb-4">
        <div class="flex items-center gap-2">
          <Radio size={16} class="text-gray-500 dark:text-dark-text-muted" />
          <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Bots</h2>
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
            New Bot
          </button>
        </div>
      </div>

      <!-- Inline Form -->
      {#if showForm}
        <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
          <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base/50">
            <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
              {editingId ? `Edit: ${formName || formPlatform}` : 'New Bot'}
            </span>
            <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-600 dark:text-dark-text-muted dark:hover:text-dark-text-secondary transition-colors">
              <X size={14} />
            </button>
          </div>

          <form onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
            <!-- Platform -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-platform" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Platform</label>
              <select
                id="form-platform"
                bind:value={formPlatform}
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text"
              >
                <option value="discord">Discord</option>
                <option value="telegram">Telegram</option>
              </select>
            </div>

            <!-- Name -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-name" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Name</label>
              <input
                id="form-name"
                type="text"
                bind:value={formName}
                placeholder="e.g., My Discord Bot"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>

            <!-- Token -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-token" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Token</label>
              <input
                id="form-token"
                type="password"
                bind:value={formToken}
                placeholder="Bot token"
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
              />
            </div>

            <!-- Default Agent -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-agent" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Default Agent</label>
              <select
                id="form-agent"
                bind:value={formDefaultAgentID}
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text"
              >
                <option value="">Select an agent...</option>
                {#each agents as a}
                  <option value={a.id}>{a.name}</option>
                {/each}
              </select>
            </div>

            <!-- Enabled -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Enabled</span>
              <label class="col-span-3 flex items-center gap-2 cursor-pointer">
                <input type="checkbox" bind:checked={formEnabled} class="text-gray-900 dark:text-accent focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:border-dark-border-subtle" />
                <span class="text-sm text-gray-600 dark:text-dark-text-secondary">Start bot on save</span>
              </label>
            </div>

            <!-- Channel/Agent Overrides -->
            <div class="grid grid-cols-4 gap-3 items-start">
              <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">
                {formPlatform === 'discord' ? 'Channel' : 'Chat'} Overrides
              </span>
              <div class="col-span-3 space-y-2">
                {#each formChannelAgents as entry, i}
                  <div class="flex gap-2 items-center">
                    <input
                      type="text"
                      bind:value={entry.key}
                      placeholder={formPlatform === 'discord' ? 'Channel ID' : 'Chat ID'}
                      class="flex-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
                    />
                    <select
                      bind:value={entry.value}
                      class="flex-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text"
                    >
                      <option value="">Select agent...</option>
                      {#each agents as a}
                        <option value={a.id}>{a.name}</option>
                      {/each}
                    </select>
                    <button
                      type="button"
                      onclick={() => removeChannelAgent(i)}
                      class="p-1 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 hover:text-red-600 dark:text-dark-text-muted dark:hover:text-red-400 transition-colors"
                      title="Remove"
                    >
                      <X size={14} />
                    </button>
                  </div>
                {/each}
                <button
                  type="button"
                  onclick={addChannelAgent}
                  class="flex items-center gap-1.5 text-sm text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
                >
                  <Plus size={12} />
                  Add override
                </button>
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

      <!-- Bot list -->
      {#if loading || bots.length > 0 || !showForm}
        <DataTable
          items={bots}
          {loading}
          {total}
          {limit}
          bind:offset
          onchange={loadData}
          onsearch={handleSearch}
          searchPlaceholder="Search by name..."
          emptyIcon={Radio}
          emptyTitle="No bots configured"
          emptyDescription="Add a Discord or Telegram bot to connect your agents to messaging platforms"
        >
          {#snippet header()}
            <SortableHeader field="platform" label="Platform" {sorts} onsort={handleSort} />
            <SortableHeader field="name" label="Name" {sorts} onsort={handleSort} />
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Agent</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Token</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Status</th>
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-32"></th>
          {/snippet}

          {#snippet row(bot)}
            <tr class="hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors">
              <td class="px-4 py-2.5">
                <span class={[
                  'px-2 py-0.5 text-xs font-medium',
                  bot.platform === 'discord'
                    ? 'bg-indigo-100 text-indigo-700 dark:bg-indigo-900/30 dark:text-indigo-300'
                    : 'bg-sky-100 text-sky-700 dark:bg-sky-900/30 dark:text-sky-300'
                ]}>
                  {bot.platform}
                </span>
              </td>
              <td class="px-4 py-2.5 font-medium text-gray-900 dark:text-dark-text">{bot.name || '-'}</td>
              <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted">
                {bot.default_agent_id ? agentName(bot.default_agent_id) : '-'}
              </td>
              <td class="px-4 py-2.5 text-xs font-mono text-gray-400 dark:text-dark-text-muted">
                {maskToken(bot.token)}
              </td>
              <td class="px-4 py-2.5">
                {#if bot.enabled}
                  <span class="px-2 py-0.5 text-xs font-medium bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300">enabled</span>
                {:else}
                  <span class="px-2 py-0.5 text-xs font-medium bg-gray-100 text-gray-500 dark:bg-dark-elevated dark:text-dark-text-muted">disabled</span>
                {/if}
              </td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex justify-end gap-1">
                  <button
                    onclick={() => openEdit(bot)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
                    title="Edit"
                  >
                    <Pencil size={14} />
                  </button>
                  {#if deleteConfirm === bot.id}
                    <button
                      onclick={() => handleDelete(bot.id)}
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
                      onclick={() => (deleteConfirm = bot.id)}
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
