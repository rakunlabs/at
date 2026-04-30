<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { listBotConfigs, createBotConfig, updateBotConfig, deleteBotConfig, startBot, stopBot, getBotStatus, type BotConfig, type BotCustomCommand, type BotStatus } from '@/lib/api/bots';
  import { listAgents, type Agent } from '@/lib/api/agents';
  import { listOrganizations, type Organization } from '@/lib/api/organizations';
  import { Trash2, Plus, X, Pencil, Radio, RefreshCw, Save, Play, Square } from 'lucide-svelte';
  import { toggleSort, buildSortParam } from '@/lib/helper/sort';
  import DataTable from '@/lib/components/DataTable.svelte';
  import SortableHeader, { type SortEntry } from '@/lib/components/SortableHeader.svelte';

  storeNavbar.title = 'Bots';

  // ─── State ───

  let bots = $state<BotConfig[]>([]);
  let agents = $state<Agent[]>([]);
  let orgs = $state<Organization[]>([]);
  let loading = $state(true);
  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let deleteConfirm = $state<string | null>(null);
  let saving = $state(false);
  let searchQuery = $state('');
  let sorts = $state<SortEntry[]>([]);
  let botStatuses = $state<Record<string, BotStatus>>({});

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
  let formAccessMode = $state('open');
  let formPendingApproval = $state(false);
  let formAllowedUsers = $state<{ value: string }[]>([]);
  let formPendingUsers = $state<string[]>([]);
  let formChannelAgents = $state<{ key: string; value: string }[]>([]);
  let formAllowedAgentIDs = $state<string[]>([]);
  let formUserContainers = $state(false);
  let formContainerImage = $state('at-agent-runtime:latest');
  let formContainerCpu = $state('1');
  let formContainerMemory = $state('2g');
  let formSpeechToText = $state('openai');
  let formWhisperModel = $state('base');
  let formCustomCommands = $state<BotCustomCommand[]>([]);

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

      const [bResult, aResult, oResult] = await Promise.all([
        listBotConfigs(params),
        listAgents({ _limit: 500 }),
        listOrganizations({ _limit: 200 }),
      ]);
      bots = bResult.data || [];
      total = bResult.meta?.total || 0;
      agents = aResult.data || [];
      orgs = oResult.data || [];
      await loadStatuses();
    } catch (e: any) {
      addToast(e?.message || 'Failed to load data', 'alert');
    } finally {
      loading = false;
    }
  }

  async function loadStatuses() {
    const newStatuses: Record<string, BotStatus> = {};
    for (const bot of bots) {
      try {
        newStatuses[bot.id] = await getBotStatus(bot.id);
      } catch {
        newStatuses[bot.id] = { running: false };
      }
    }
    botStatuses = newStatuses;
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
    formAccessMode = 'open';
    formPendingApproval = false;
    formAllowedUsers = [];
    formPendingUsers = [];
    formChannelAgents = [];
    formAllowedAgentIDs = [];
    formUserContainers = false;
    formContainerImage = 'at-agent-runtime:latest';
    formContainerCpu = '1';
    formContainerMemory = '2g';
    formSpeechToText = 'openai';
    formWhisperModel = 'base';
    formCustomCommands = [];
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
    formAccessMode = bot.access_mode || 'open';
    formPendingApproval = bot.pending_approval || false;
    formAllowedUsers = (bot.allowed_users || []).map((v) => ({ value: v }));
    formPendingUsers = bot.pending_users || [];
    formChannelAgents = Object.entries(bot.channel_agents || {}).map(([key, value]) => ({ key, value }));
    formAllowedAgentIDs = bot.allowed_agent_ids || [];
    formUserContainers = bot.user_containers || false;
    formContainerImage = bot.container_image || 'at-agent-runtime:latest';
    formContainerCpu = bot.container_cpu || '1';
    formContainerMemory = bot.container_memory || '2g';
    formSpeechToText = bot.speech_to_text || 'openai';
    formWhisperModel = bot.whisper_model || 'base';
    formCustomCommands = (bot.custom_commands || []).map((c) => ({ ...c }));
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
        allowed_agent_ids: formAllowedAgentIDs.filter(Boolean),
        access_mode: formAccessMode,
        pending_approval: formPendingApproval,
        allowed_users: formAllowedUsers.map((e) => e.value.trim()).filter(Boolean),
        pending_users: formPendingUsers,
        enabled: formEnabled,
        user_containers: formUserContainers,
        container_image: formUserContainers ? formContainerImage : undefined,
        container_cpu: formUserContainers ? formContainerCpu : undefined,
        container_memory: formUserContainers ? formContainerMemory : undefined,
        speech_to_text: formSpeechToText,
        whisper_model: formSpeechToText !== 'openai' && formSpeechToText !== 'none' ? formWhisperModel : undefined,
        custom_commands: formCustomCommands
          .map((c) => ({
            command: (c.command || '').trim().replace(/^\//, ''),
            description: (c.description || '').trim() || undefined,
            organization_id: c.organization_id || undefined,
            agent_id: c.agent_id || undefined,
            brief: (c.brief || '').trim() || undefined,
            title_prefix: (c.title_prefix || '').trim() || undefined,
            max_iterations: c.max_iterations && c.max_iterations > 0 ? c.max_iterations : undefined,
          }))
          .filter((c) => c.command.length > 0),
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

  async function handleStartBot(id: string) {
    try {
      await startBot(id);
      addToast('Bot started');
      await loadStatuses();
      await loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to start bot', 'alert');
    }
  }

  async function handleStopBot(id: string) {
    try {
      await stopBot(id);
      addToast('Bot stopped');
      await loadStatuses();
      await loadData();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to stop bot', 'alert');
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

  // ─── Custom commands management ───

  function addCustomCommand() {
    formCustomCommands = [
      ...formCustomCommands,
      { command: '', description: '', organization_id: '', agent_id: '', brief: '', title_prefix: '', max_iterations: 0 },
    ];
  }

  function removeCustomCommand(i: number) {
    formCustomCommands = formCustomCommands.filter((_, idx) => idx !== i);
  }

  // ─── Allowed/pending users management ───

  function addAllowedUser() {
    formAllowedUsers = [...formAllowedUsers, { value: '' }];
  }

  function removeAllowedUser(i: number) {
    formAllowedUsers = formAllowedUsers.filter((_, idx) => idx !== i);
  }

  function approvePendingUser(userID: string) {
    formPendingUsers = formPendingUsers.filter((u) => u !== userID);
    formAllowedUsers = [...formAllowedUsers, { value: userID }];
  }

  function denyPendingUser(userID: string) {
    formPendingUsers = formPendingUsers.filter((u) => u !== userID);
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

            <!-- Platform description -->
            {#if formPlatform === 'telegram'}
              <div class="col-span-4 bg-blue-50 dark:bg-blue-900/20 border border-blue-200 dark:border-blue-800 px-4 py-3 text-xs space-y-2">
                <div class="font-medium text-blue-700 dark:text-blue-400">Telegram Bot Setup</div>
                <ol class="list-decimal list-inside text-blue-600 dark:text-blue-300 space-y-1">
                  <li>Open <a href="https://t.me/BotFather" target="_blank" class="underline font-medium">@BotFather</a> on Telegram</li>
                  <li>Send <code class="bg-blue-100 dark:bg-blue-900/40 px-1 rounded font-mono">/newbot</code> and follow the steps to get a token</li>
                  <li>Paste the token in the Token field above</li>
                  <li>Set commands in BotFather with <code class="bg-blue-100 dark:bg-blue-900/40 px-1 rounded font-mono">/setcommands</code>:</li>
                </ol>
                <div class="bg-white dark:bg-dark-elevated border border-blue-200 dark:border-blue-800 rounded p-2 font-mono text-[11px] text-gray-700 dark:text-dark-text-secondary leading-relaxed">
                  new - Create a background task<br>
                  tasks - List recent tasks<br>
                  status - Check task status<br>
                  result - Get task output and video<br>
                  pick - Select task to chat about<br>
                  current - Show active task<br>
                  reset - Clear conversation<br>
                  agents - List available agents<br>
                  switch - Switch to a different agent<br>
                  login - Connect your Google account<br>
                  help - Show available commands
                </div>
                <div class="text-blue-500 dark:text-blue-400">
                  Copy the commands above and paste them when BotFather asks for the command list.
                </div>
                <div class="font-medium text-blue-700 dark:text-blue-400 mt-2">Available Commands</div>
                <div class="text-blue-600 dark:text-blue-300 space-y-0.5">
                  <div><code class="font-mono font-medium">/new &lt;topic&gt;</code> — Creates a background task and runs it via the org delegation system. Returns a task ID you can track.</div>
                  <div><code class="font-mono font-medium">/tasks</code> — List recent tasks with status and clickable IDs</div>
                  <div><code class="font-mono font-medium">/status [id]</code> — Check task status. No ID = active task</div>
                  <div><code class="font-mono font-medium">/result [id]</code> — Get task output + sends video/images as raw files</div>
                  <div><code class="font-mono font-medium">/pick &lt;id&gt;</code> — Select a task to chat about. Messages include task context. Use <code>/pick</code> alone to deselect</div>
                  <div><code class="font-mono font-medium">/current</code> — Show which task is currently active</div>
                  <div><code class="font-mono font-medium">/reset</code> — Clears conversation history for normal chat</div>
                  <div><code class="font-mono font-medium">/agents</code> — Lists all agents the user can switch to</div>
                  <div><code class="font-mono font-medium">/switch &lt;name&gt;</code> — Switches to a different agent and clears session</div>
                  <div><code class="font-mono font-medium">/login [provider]</code> — Generates an OAuth login link (default: google)</div>
                  <div><code class="font-mono font-medium">/help</code> — Shows the list of available commands</div>
                </div>
                <div class="mt-2 p-2 bg-blue-100 dark:bg-blue-900/30 rounded text-blue-700 dark:text-blue-300 space-y-1">
                  <div><span class="font-medium">Workflow:</span></div>
                  <div>1. <code class="font-mono">/new top 5 deadliest animals</code> → Creates task YTS-1</div>
                  <div>2. Task runs in background, bot notifies when done/failed</div>
                  <div>3. <code class="font-mono">/status</code> → Check if done</div>
                  <div>4. <code class="font-mono">/result</code> → Get the video file</div>
                  <div>5. <code class="font-mono">/new how volcanoes work</code> → New task YTS-2</div>
                  <div>6. <code class="font-mono">/tasks</code> → See all tasks</div>
                  <div>7. <code class="font-mono">/current</code> → See which task is active</div>
                </div>
              </div>
            {:else if formPlatform === 'discord'}
              <div class="col-span-4 bg-indigo-50 dark:bg-indigo-900/20 border border-indigo-200 dark:border-indigo-800 px-4 py-3 text-xs space-y-2">
                <div class="font-medium text-indigo-700 dark:text-indigo-400">Discord Bot Setup</div>
                <ol class="list-decimal list-inside text-indigo-600 dark:text-indigo-300 space-y-1">
                  <li>Go to <a href="https://discord.com/developers/applications" target="_blank" class="underline font-medium">Discord Developer Portal</a></li>
                  <li>Create a new application and add a Bot</li>
                  <li>Copy the bot token and paste it in the Token field</li>
                  <li>Enable Message Content Intent under Bot → Privileged Gateway Intents</li>
                  <li>Invite the bot to your server with OAuth2 URL Generator (scopes: bot, permissions: Send Messages)</li>
                </ol>
              </div>
            {/if}

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
            <div class="grid grid-cols-4 gap-3 items-start">
              <label for="form-agent" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Default Agent</label>
              <div class="col-span-3 space-y-1">
                <select
                  id="form-agent"
                  bind:value={formDefaultAgentID}
                  class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text"
                >
                  <option value="">Select an agent...</option>
                  {#each agents as a (a.id)}
                    <option value={a.id}>{a.name}</option>
                  {/each}
                  {#if formDefaultAgentID && !agents.find((a) => a.id === formDefaultAgentID)}
                    <!-- Saved agent no longer in the list (deleted, paginated out, or still loading). Keep it selectable so we don't silently lose it on save. -->
                    <option value={formDefaultAgentID}>Unknown agent ({formDefaultAgentID.slice(0, 12)}…)</option>
                  {/if}
                </select>
                <div class="text-[10px] text-gray-400 dark:text-dark-text-muted">
                  This agent receives all messages by default. Use Channel/Chat Overrides below to route specific channels to other agents.
                </div>
              </div>
            </div>

            <!-- Enabled -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Enabled</span>
              <label class="col-span-3 flex items-center gap-2 cursor-pointer">
                <input type="checkbox" bind:checked={formEnabled} class="text-gray-900 dark:text-accent focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:border-dark-border-subtle" />
                <span class="text-sm text-gray-600 dark:text-dark-text-secondary">Start bot on save</span>
              </label>
            </div>

            <!-- Access Mode -->
            <div class="grid grid-cols-4 gap-3 items-center">
              <label for="form-access-mode" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Access Mode</label>
              <select
                id="form-access-mode"
                bind:value={formAccessMode}
                class="col-span-3 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text"
              >
                <option value="open">Open (everyone)</option>
                <option value="allowlist">Allowlist (approved users only)</option>
              </select>
            </div>

            <!-- Allowlist settings (shown when allowlist mode) -->
            {#if formAccessMode === 'allowlist'}
              <!-- Pending Approval toggle -->
              <div class="grid grid-cols-4 gap-3 items-center">
                <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Pending Approval</span>
                <label class="col-span-3 flex items-center gap-2 cursor-pointer">
                  <input type="checkbox" bind:checked={formPendingApproval} class="text-gray-900 dark:text-accent focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:border-dark-border-subtle" />
                  <span class="text-sm text-gray-600 dark:text-dark-text-secondary">Unknown users get a "pending approval" reply and appear in the list below</span>
                </label>
              </div>

              <div class="grid grid-cols-4 gap-3 items-start">
                <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Allowed Users</span>
                <div class="col-span-3 space-y-2">
                  {#each formAllowedUsers as entry, i}
                    <div class="flex gap-2 items-center">
                      <input
                        type="text"
                        bind:value={entry.value}
                        placeholder="User ID"
                        class="flex-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle transition-colors dark:text-dark-text dark:placeholder:text-dark-text-muted"
                      />
                      <button
                        type="button"
                        onclick={() => removeAllowedUser(i)}
                        class="p-1 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 hover:text-red-600 dark:text-dark-text-muted dark:hover:text-red-400 transition-colors"
                        title="Remove"
                      >
                        <X size={14} />
                      </button>
                    </div>
                  {/each}
                  <button
                    type="button"
                    onclick={addAllowedUser}
                    class="flex items-center gap-1.5 text-sm text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
                  >
                    <Plus size={12} />
                    Add user ID
                  </button>
                </div>
              </div>

              <!-- Pending Users (shown when pending approval is on and there are pending users) -->
              {#if formPendingApproval && editingId && formPendingUsers.length > 0}
                <div class="grid grid-cols-4 gap-3 items-start">
                  <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Pending Users</span>
                  <div class="col-span-3 space-y-2">
                    {#each formPendingUsers as userID}
                      <div class="flex gap-2 items-center">
                        <span class="flex-1 px-3 py-1.5 text-sm font-mono bg-gray-50 dark:bg-dark-base/50 border border-gray-200 dark:border-dark-border text-gray-700 dark:text-dark-text-secondary">
                          {userID}
                        </span>
                        <button
                          type="button"
                          onclick={() => approvePendingUser(userID)}
                          class="px-2 py-1 text-xs bg-green-600 text-white hover:bg-green-700 transition-colors"
                        >
                          Approve
                        </button>
                        <button
                          type="button"
                          onclick={() => denyPendingUser(userID)}
                          class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors"
                        >
                          Deny
                        </button>
                      </div>
                    {/each}
                  </div>
                </div>
              {/if}
            {/if}

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

            <!-- Allowed Agents for /switch -->
            <div class="grid grid-cols-4 gap-3 items-start">
              <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Switchable Agents</span>
              <div class="col-span-3 space-y-2">
                <div class="text-[10px] text-gray-500 dark:text-dark-text-muted">
                  Agents users may pick with <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1 rounded">/switch &lt;name&gt;</code> and see in <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1 rounded">/agents</code>.
                  Leave empty to <strong>disable</strong> agent switching entirely (everyone stays on the Default Agent).
                </div>
                {#if agents.length === 0}
                  <div class="text-xs text-gray-400 dark:text-dark-text-muted italic px-2 py-3 border border-dashed border-gray-200 dark:border-dark-border">
                    No agents available. Create at least one agent first.
                  </div>
                {:else}
                  <div class="border border-gray-200 dark:border-dark-border max-h-48 overflow-y-auto bg-white dark:bg-dark-elevated">
                    {#each agents as a (a.id)}
                      <label class="flex items-center gap-2 px-3 py-1.5 hover:bg-gray-50 dark:hover:bg-dark-base/50 cursor-pointer border-b border-gray-100 dark:border-dark-border last:border-b-0">
                        <input
                          type="checkbox"
                          checked={formAllowedAgentIDs.includes(a.id)}
                          onchange={(e) => {
                            const checked = (e.currentTarget as HTMLInputElement).checked;
                            if (checked) {
                              if (!formAllowedAgentIDs.includes(a.id)) formAllowedAgentIDs = [...formAllowedAgentIDs, a.id];
                            } else {
                              formAllowedAgentIDs = formAllowedAgentIDs.filter((id) => id !== a.id);
                            }
                          }}
                          class="text-gray-900 dark:text-accent focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:border-dark-border-subtle"
                        />
                        <span class="text-sm text-gray-700 dark:text-dark-text-secondary flex-1">{a.name}</span>
                        <span class="text-[10px] font-mono text-gray-400 dark:text-dark-text-muted">{a.id.slice(0, 12)}…</span>
                      </label>
                    {/each}
                  </div>
                  <div class="flex items-center gap-2 text-xs">
                    <button
                      type="button"
                      onclick={() => formAllowedAgentIDs = agents.map((a) => a.id)}
                      class="text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
                    >Select all</button>
                    <span class="text-gray-300 dark:text-dark-border">·</span>
                    <button
                      type="button"
                      onclick={() => formAllowedAgentIDs = []}
                      class="text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
                    >Clear all</button>
                    <span class="ml-auto text-gray-400 dark:text-dark-text-muted">
                      {formAllowedAgentIDs.length} selected
                    </span>
                  </div>
                  {#if formDefaultAgentID && !formAllowedAgentIDs.includes(formDefaultAgentID)}
                    <div class="text-[10px] text-amber-600 dark:text-amber-400 px-2 py-1 bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-800/40">
                      Tip: the Default Agent is not in this list. Users who <code class="font-mono">/switch</code> away from it won't be able to switch back.
                    </div>
                  {/if}
                {/if}
              </div>
            </div>

            <!-- Speech-to-Text -->
            <div class="grid grid-cols-4 gap-3 items-start">
              <label class="contents">
                <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Voice Transcription</span>
              <div class="col-span-3 space-y-2">
                <select
                  bind:value={formSpeechToText}
                  class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text transition-colors"
                >
                  <option value="openai">OpenAI Whisper API (cloud, best quality)</option>
                  <option value="local">Local Whisper (free, uses CPU/GPU)</option>
                  <option value="faster-whisper">Faster-Whisper (free, optimized)</option>
                  <option value="none">Disabled</option>
                </select>
                {#if formSpeechToText === 'local' || formSpeechToText === 'faster-whisper'}
                  <select
                    bind:value={formWhisperModel}
                    class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:text-dark-text transition-colors"
                  >
                    <option value="tiny">tiny (39M, fastest)</option>
                    <option value="base">base (74M, fast)</option>
                    <option value="small">small (244M, good)</option>
                    <option value="medium">medium (769M, better)</option>
                    <option value="large-v3">large-v3 (1.5G, best)</option>
                  </select>
                  <div class="text-[10px] text-gray-400 dark:text-dark-text-muted">
                    Uses <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1 rounded">uvx</code> to run {formSpeechToText === 'faster-whisper' ? 'faster-whisper' : 'openai-whisper'} locally. First run downloads the model.
                  </div>
                {:else if formSpeechToText === 'openai'}
                  <div class="text-[10px] text-gray-400 dark:text-dark-text-muted">
                    Uses OpenAI API (~$0.006/min). Requires <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1 rounded">openai_api_key</code> variable.
                  </div>
                {:else}
                  <div class="text-[10px] text-gray-400 dark:text-dark-text-muted">
                    Voice messages will be attached as files instead of transcribed.
                  </div>
                {/if}
              </div>
              </label>
            </div>

            <!-- Per-User Container Isolation -->
            <div class="border border-gray-200 dark:border-dark-border-subtle p-3 space-y-3">
              <label class="flex items-center gap-2 cursor-pointer">
                <input type="checkbox" bind:checked={formUserContainers} class="w-3.5 h-3.5 dark:accent-accent" />
                <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Per-user container isolation</span>
              </label>
              {#if formUserContainers}
                <div class="text-[10px] text-gray-400 dark:text-dark-text-muted mb-2">
                  Each bot user gets their own isolated Docker container. Files, packages, and state are completely separate between users.
                </div>
                <div class="grid grid-cols-3 gap-2">
                  <label class="block">
                    <span class="text-[10px] text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Image</span>
                    <input type="text" bind:value={formContainerImage} placeholder="at-agent-runtime:latest"
                      class="w-full px-2 py-1 text-xs font-mono border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text" />
                  </label>
                  <label class="block">
                    <span class="text-[10px] text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">CPU</span>
                    <input type="text" bind:value={formContainerCpu} placeholder="1"
                      class="w-full px-2 py-1 text-xs font-mono border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text" />
                  </label>
                  <label class="block">
                    <span class="text-[10px] text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Memory</span>
                    <input type="text" bind:value={formContainerMemory} placeholder="2g"
                      class="w-full px-2 py-1 text-xs font-mono border border-gray-300 dark:border-dark-border-subtle rounded focus:outline-none focus:ring-1 focus:ring-gray-400 dark:bg-dark-elevated dark:text-dark-text" />
                  </label>
                </div>
                <div class="text-[10px] text-gray-400 dark:text-dark-text-muted">
                  Build image: <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1 rounded">docker build -f Dockerfile.agent-runtime -t {formContainerImage} .</code>
                </div>
              {/if}
            </div>

            <!-- Custom Commands -->
            {#if formPlatform === 'telegram'}
              <div class="grid grid-cols-4 gap-3 items-start">
                <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Custom Commands</span>
                <div class="col-span-3 space-y-3">
                  <div class="text-[10px] text-gray-500 dark:text-dark-text-muted">
                    Add slash commands like <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1 rounded">/asmr</code> or <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1 rounded">/silent</code>. Each command creates a background task. Use <code class="font-mono bg-gray-100 dark:bg-dark-elevated px-1 rounded">{'{args}'}</code> in the brief to insert whatever the user typed after the command.
                  </div>
                  {#each formCustomCommands as cmd, i}
                    <div class="border border-gray-200 dark:border-dark-border-subtle p-3 space-y-2 bg-gray-50/50 dark:bg-dark-base/40">
                      <div class="flex items-center gap-2">
                        <span class="text-xs font-mono text-gray-500 dark:text-dark-text-muted">/</span>
                        <input
                          type="text"
                          bind:value={cmd.command}
                          placeholder="asmr"
                          class="flex-1 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2 py-1 text-sm font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 dark:text-dark-text"
                        />
                        <input
                          type="text"
                          bind:value={cmd.description}
                          placeholder="Short description (shown in /help)"
                          class="flex-[2] border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2 py-1 text-sm focus:outline-none focus:ring-1 focus:ring-gray-400 dark:text-dark-text"
                        />
                        <button
                          type="button"
                          onclick={() => removeCustomCommand(i)}
                          class="p-1 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 hover:text-red-600 dark:text-dark-text-muted dark:hover:text-red-400 transition-colors"
                          title="Remove command"
                        >
                          <X size={14} />
                        </button>
                      </div>
                      <div class="grid grid-cols-2 gap-2">
                        <label class="block">
                          <span class="text-[10px] text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Route to organization</span>
                          <select
                            bind:value={cmd.organization_id}
                            class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2 py-1 text-sm focus:outline-none focus:ring-1 focus:ring-gray-400 dark:text-dark-text"
                          >
                            <option value="">— none —</option>
                            {#each orgs as o (o.id)}
                              <option value={o.id}>{o.name}</option>
                            {/each}
                          </select>
                        </label>
                        <label class="block">
                          <span class="text-[10px] text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">…or assign to agent</span>
                          <select
                            bind:value={cmd.agent_id}
                            class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2 py-1 text-sm focus:outline-none focus:ring-1 focus:ring-gray-400 dark:text-dark-text"
                          >
                            <option value="">— bot's default —</option>
                            {#each agents as a (a.id)}
                              <option value={a.id}>{a.name}</option>
                            {/each}
                          </select>
                        </label>
                      </div>
                      <label class="block">
                        <span class="text-[10px] text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Brief template</span>
                        <textarea
                          bind:value={cmd.brief}
                          placeholder={'Generate a 25-minute ASMR session. {args}'}
                          rows="3"
                          class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2 py-1 text-sm font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 dark:text-dark-text dark:placeholder:text-dark-text-muted resize-y"
                        ></textarea>
                      </label>
                      <div class="grid grid-cols-2 gap-2">
                        <label class="block">
                          <span class="text-[10px] text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Title prefix</span>
                          <input
                            type="text"
                            bind:value={cmd.title_prefix}
                            placeholder="[ASMR]"
                            class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2 py-1 text-sm focus:outline-none focus:ring-1 focus:ring-gray-400 dark:text-dark-text"
                          />
                        </label>
                        <label class="block">
                          <span class="text-[10px] text-gray-500 dark:text-dark-text-muted uppercase tracking-wider block mb-0.5">Max iterations (0 = agent default)</span>
                          <input
                            type="number"
                            min="0"
                            bind:value={cmd.max_iterations}
                            placeholder="0"
                            class="w-full border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-elevated px-2 py-1 text-sm font-mono focus:outline-none focus:ring-1 focus:ring-gray-400 dark:text-dark-text"
                          />
                        </label>
                      </div>
                    </div>
                  {/each}
                  <button
                    type="button"
                    onclick={addCustomCommand}
                    class="flex items-center gap-1.5 text-sm text-gray-500 hover:text-gray-900 dark:text-dark-text-muted dark:hover:text-dark-text transition-colors"
                  >
                    <Plus size={12} />
                    Add custom command
                  </button>
                </div>
              </div>
            {/if}

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
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Access</th>
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
                <span class="text-xs text-gray-500 dark:text-dark-text-muted">
                  {bot.access_mode === 'allowlist' ? 'allowlist' : 'open'}
                </span>
                {#if bot.pending_users?.length}
                  <span class="ml-1 px-1.5 py-0.5 text-xs font-medium bg-amber-100 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300">
                    {bot.pending_users.length} pending
                  </span>
                {/if}
              </td>
              <td class="px-4 py-2.5">
                {#if botStatuses[bot.id]?.running}
                  <span class="inline-flex items-center gap-1.5 px-2 py-0.5 text-xs font-medium bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-300">
                    <span class="w-1.5 h-1.5 rounded-full bg-green-500 animate-pulse"></span>
                    Running
                  </span>
                {:else if bot.enabled}
                  <span class="px-2 py-0.5 text-xs font-medium bg-yellow-100 text-yellow-700 dark:bg-yellow-900/30 dark:text-yellow-300">Enabled</span>
                {:else}
                  <span class="px-2 py-0.5 text-xs font-medium bg-gray-100 text-gray-500 dark:bg-dark-elevated dark:text-dark-text-muted">Stopped</span>
                {/if}
              </td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex justify-end gap-1">
                  {#if botStatuses[bot.id]?.running}
                    <button
                      onclick={() => handleStopBot(bot.id)}
                      class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 hover:text-red-600 dark:text-dark-text-muted dark:hover:text-red-400 transition-colors"
                      title="Stop bot"
                    >
                      <Square size={14} />
                    </button>
                  {:else}
                    <button
                      onclick={() => handleStartBot(bot.id)}
                      disabled={!bot.token}
                      class="p-1.5 hover:bg-green-50 dark:hover:bg-green-900/20 text-gray-400 hover:text-green-600 dark:text-dark-text-muted dark:hover:text-green-400 transition-colors disabled:opacity-30"
                      title="Start bot"
                    >
                      <Play size={14} />
                    </button>
                  {/if}
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
