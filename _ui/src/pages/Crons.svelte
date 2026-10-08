<script lang="ts">
  import LoadIssues from '@/lib/components/LoadIssues.svelte';
  import ExecutionBinding from '@/lib/components/ExecutionBinding.svelte';
  import { createPageLoader } from '@/lib/helper/page-load.svelte';
  const pageLoad = createPageLoader();
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listAllTriggers,
    createTrigger,
    updateTrigger,
    deleteTrigger,
    runTriggerNow,
    listCronRuns,
    getCronRun,
    type Trigger,
    type CronRun,
  } from '@/lib/api/triggers';
  import { listWorkflows, getWorkflow, type Workflow, type WorkflowNode } from '@/lib/api/workflows';
  import { listOrganizations, type Organization } from '@/lib/api/organizations';
  import { listBotConfigs, type BotConfig } from '@/lib/api/bots';
  import {
    Clock,
    Plus,
    Pencil,
    Trash2,
    X,
    Save,
    RefreshCw,
    Power,
    PowerOff,
    Play,
    History,
    ChevronRight,
    ChevronDown,
  } from 'lucide-svelte';
  import { formatDate, formatDateTime } from '@/lib/helper/format';
  import { onDestroy } from 'svelte';

  storeNavbar.title = 'Cron schedules';

  // ─── State ───

  let triggers = $state<Trigger[]>([]);
  let loading = $state(true);
  let tab = $state<'schedules' | 'runs'>('schedules');

  // Reference data
  let workflows = $state<Workflow[]>([]);
  let organizations = $state<Organization[]>([]);
  let bots = $state<BotConfig[]>([]);

  // Form
  let showForm = $state(false);
  let editingId = $state<string | null>(null);
  let saving = $state(false);
  let deleteConfirm = $state<string | null>(null);

  // Form fields
  let formTargetType = $state('workflow');
  let formTargetId = $state('');
  let formEntryNodeId = $state('');
  let formSchedule = $state('');
  let formTimezone = $state('');
  let formEnabled = $state(true);
  let formPayload = $state('{}');
  // Organization target: one task per tick for the head agent.
  let formTaskTitle = $state('');
  let formTaskDescription = $state('');
  let formMaxIterations = $state(0);
  let formNotifyBotId = $state('');
  let formNotifyChatId = $state('');
  const taskConfigKeys = ['task_title', 'task_description', 'max_iterations', 'notify_bot_id', 'notify_chat_id'];
  let telegramBots = $derived(bots.filter((b) => b.platform === 'telegram'));
  let notifyUsers = $derived(telegramBots.find((b) => b.id === formNotifyBotId)?.allowed_users || []);

  // Entry node selection
  let inputNodes = $state<WorkflowNode[]>([]);
  let loadingInputNodes = $state(false);

  // Schedule examples
  const scheduleExamples = [
    { label: 'Every minute', value: '* * * * *' },
    { label: 'Every 5 minutes', value: '*/5 * * * *' },
    { label: 'Every 15 minutes', value: '*/15 * * * *' },
    { label: 'Every hour', value: '0 * * * *' },
    { label: 'Every day at midnight', value: '0 0 * * *' },
    { label: 'Every day at 3am', value: '0 3 * * *' },
    { label: 'Every Monday at 9am', value: '0 9 * * 1' },
    { label: 'Every 1st of month', value: '0 0 1 * *' },
  ];

  // ─── Load ───

  async function load() {
    loading = true;
    pageLoad.reset();
    try {
      await Promise.all([
        pageLoad.load('Schedules', () => listAllTriggers({ type: 'cron' }), result => { triggers = result || []; }, 'workflow_builder'),
        pageLoad.load('Workflows', () => listWorkflows({ _limit: 1000 }), result => { workflows = result.data || []; }, 'workflow_builder'),
        pageLoad.load('Organizations', () => listOrganizations({ _limit: 1000 }), result => { organizations = result.data || []; }, 'organizations'),
        pageLoad.load('Bots', () => listBotConfigs({ _limit: 1000 }), result => { bots = result.data || []; }, 'bots'),
      ]);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load cron jobs', 'alert');
    } finally {
      loading = false;
    }
  }

  load();

  // ─── Helpers ───

  function getTargetName(t: Trigger): string {
    if (t.target_type === 'workflow') {
      return workflows.find(w => w.id === t.target_id)?.name || t.target_id;
    }
    if (t.target_type === 'organization') {
      const org = organizations.find(o => o.id === t.target_id)?.name || t.target_id;
      return `${org} → task: ${(t.config?.task_title as string) || ''}`;
    }
    return t.target_id;
  }

  function describeSchedule(schedule: string): string {
    if (!schedule) return '';
    const match = scheduleExamples.find(e => e.value === schedule);
    if (match) return match.label;
    // Try simple patterns
    const parts = schedule.trim().split(/\s+/);
    if (parts.length !== 5) return schedule;
    if (parts[0].startsWith('*/')) return `Every ${parts[0].slice(2)} min`;
    if (parts[0] === '0' && parts[1] === '*') return 'Hourly';
    if (parts[0] === '0' && parts[1] === '0') return 'Daily at midnight';
    return schedule;
  }

  // ─── Form ───

  function resetForm() {
    formTargetType = 'workflow';
    formTargetId = '';
    formEntryNodeId = '';
    formSchedule = '';
    formTimezone = '';
    formEnabled = true;
    formPayload = '{}';
    formTaskTitle = '';
    formTaskDescription = '';
    formMaxIterations = 0;
    formNotifyBotId = '';
    formNotifyChatId = '';
    editingId = null;
    showForm = false;
    inputNodes = [];
  }

  function openCreate() {
    resetForm();
    showForm = true;
  }

  async function openEdit(t: Trigger) {
    resetForm();
    editingId = t.id;
    formTargetType = t.target_type || 'workflow';
    formTargetId = t.target_id;
    formEntryNodeId = t.entry_node_id || '';
    formSchedule = (t.config?.schedule as string) || '';
    formTimezone = (t.config?.timezone as string) || '';
    formEnabled = t.enabled;
    const payload = { ...t.config };
    delete payload.schedule;
    delete payload.timezone;
    if (formTargetType === 'organization') {
      formTaskTitle = (t.config?.task_title as string) || '';
      formTaskDescription = (t.config?.task_description as string) || '';
      formMaxIterations = Number(t.config?.max_iterations) || 0;
      formNotifyBotId = (t.config?.notify_bot_id as string) || '';
      formNotifyChatId = String(t.config?.notify_chat_id ?? '');
      for (const k of taskConfigKeys) delete payload[k];
    }
    formPayload = Object.keys(payload).length > 0 ? JSON.stringify(payload, null, 2) : '{}';
    showForm = true;
    if (formTargetType === 'workflow' && formTargetId) {
      await loadInputNodes(formTargetId);
    }
  }

  async function loadInputNodes(workflowId: string) {
    if (!workflowId) {
      inputNodes = [];
      return;
    }
    loadingInputNodes = true;
    try {
      const wf = await getWorkflow(workflowId);
      inputNodes = (wf.graph?.nodes || []).filter((n: WorkflowNode) => n.type === 'input');
    } catch {
      inputNodes = [];
    } finally {
      loadingInputNodes = false;
    }
  }

  async function handleTargetIdChange(newId: string) {
    formTargetId = newId;
    formEntryNodeId = '';
    if (formTargetType === 'workflow') {
      await loadInputNodes(newId);
    }
  }

  async function handleSubmit() {
    if (!formTargetId.trim()) {
      addToast('Target is required', 'warn');
      return;
    }
    if (!formSchedule.trim()) {
      addToast('Cron schedule is required', 'warn');
      return;
    }

    const isOrg = formTargetType === 'organization';
    if (isOrg && !formTaskTitle.trim()) {
      addToast('Task title is required', 'warn');
      return;
    }
    if (isOrg && !!formNotifyBotId !== !!formNotifyChatId) {
      addToast('Choose both a Telegram bot and a chat, or neither', 'warn');
      return;
    }

    let extraPayload: Record<string, any> = {};
    if (!isOrg) {
      try {
        extraPayload = JSON.parse(formPayload);
      } catch {
        addToast('Payload must be valid JSON', 'warn');
        return;
      }
    }

    saving = true;
    try {
      const config: Record<string, any> = {
        schedule: formSchedule.trim(),
        ...extraPayload,
      };
      if (formTimezone.trim()) {
        config.timezone = formTimezone.trim();
      }
      if (isOrg) {
        config.task_title = formTaskTitle.trim();
        if (formTaskDescription.trim()) config.task_description = formTaskDescription.trim();
        if (formMaxIterations > 0) config.max_iterations = formMaxIterations;
        if (formNotifyBotId) {
          config.notify_bot_id = formNotifyBotId;
          config.notify_chat_id = formNotifyChatId;
        }
      }

      const payload: Partial<Trigger> = {
        type: 'cron',
        target_type: formTargetType,
        target_id: formTargetId,
        entry_node_id: isOrg ? undefined : formEntryNodeId || undefined,
        enabled: formEnabled,
        config,
      };

      if (editingId) {
        await updateTrigger(editingId, payload);
        addToast('Cron job updated');
      } else {
        await createTrigger(payload);
        addToast('Cron job created');
      }
      resetForm();
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to save cron job', 'alert');
    } finally {
      saving = false;
    }
  }

  async function handleDelete(id: string) {
    try {
      await deleteTrigger(id);
      addToast('Cron job deleted');
      deleteConfirm = null;
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete cron job', 'alert');
    }
  }

  async function toggleEnabled(t: Trigger) {
    try {
      await updateTrigger(t.id, { enabled: !t.enabled, type: t.type, target_type: t.target_type, target_id: t.target_id, config: t.config });
      addToast(t.enabled ? 'Cron job disabled' : 'Cron job enabled');
      await load();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update cron job', 'alert');
    }
  }

  // ─── Manual run ───

  let starting = $state<string | null>(null);

  async function runNow(t: Trigger) {
    starting = t.id;
    try {
      await runTriggerNow(t.id);
      addToast('Run started');
      runsTriggerId = t.id;
      tab = 'runs';
      // The run row is written by the background run; give it a moment.
      setTimeout(() => loadRuns(), 600);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to start run', 'alert');
    } finally {
      starting = null;
    }
  }

  // ─── Run history ───

  let runsTriggerId = $state('');
  let runs = $state<CronRun[]>([]);
  let runsLoading = $state(false);
  let runsError = $state('');
  let runsHasMore = $state(false);
  let expanded = $state<Record<string, CronRun | null>>({});
  const runsPage = 30;

  $effect(() => {
    if (!runsTriggerId && triggers.length) runsTriggerId = triggers[0].id;
  });

  $effect(() => {
    if (tab === 'runs' && runsTriggerId) {
      expanded = {};
      loadRuns();
    }
  });

  async function loadRuns(more = false) {
    const id = runsTriggerId;
    if (!id) return;
    runsLoading = true;
    runsError = '';
    try {
      const before = more && runs.length ? runs[runs.length - 1].id : undefined;
      const page = await listCronRuns(id, { before, limit: runsPage });
      if (id !== runsTriggerId) return;
      runs = more ? [...runs, ...page] : page;
      runsHasMore = page.length === runsPage;
      // Refresh open logs so a running run's milestones appear.
      for (const runID of Object.keys(expanded)) {
        if (expanded[runID] && runs.find((r) => r.id === runID)?.status === 'running') loadRunDetail(runID);
      }
    } catch (e: any) {
      runsError = e?.response?.data?.message || 'Failed to load runs';
    } finally {
      runsLoading = false;
    }
  }

  async function loadRunDetail(id: string) {
    try {
      expanded = { ...expanded, [id]: await getCronRun(id) };
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load run log', 'alert');
    }
  }

  function toggleRun(id: string) {
    if (id in expanded) {
      const { [id]: _, ...rest } = expanded;
      expanded = rest;
      return;
    }
    expanded = { ...expanded, [id]: null };
    loadRunDetail(id);
  }

  // Poll while something is running and the page is visible.
  const poll = setInterval(() => {
    if (tab === 'runs' && !document.hidden && !runsLoading && runs.some((r) => r.status === 'running')) loadRuns();
  }, 10000);
  onDestroy(() => clearInterval(poll));

  function duration(r: CronRun): string {
    const start = new Date(r.started_at).getTime();
    const end = r.finished_at ? new Date(r.finished_at).getTime() : Date.now();
    const s = Math.max(0, Math.round((end - start) / 1000));
    if (s < 60) return `${s}s`;
    if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`;
    return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
  }

  const statusClass: Record<string, string> = {
    running: 'text-oc-peach',
    completed: 'text-oc-green',
    failed: 'text-oc-red',
    blocked: 'text-oc-red',
    cancelled: 'text-dark-text-muted',
  };
  const reportClass: Record<string, string> = {
    completed: 'text-oc-green',
    partial: 'text-oc-peach',
    failed: 'text-oc-red',
  };
  const levelClass: Record<string, string> = {
    system: 'text-dark-text-muted',
    milestone: 'text-dark-text',
    report: 'text-accent',
    error: 'text-oc-red',
  };

  function triggerLabel(t: Trigger): string {
    return `${getTargetName(t)} · ${t.config?.schedule || ''}`;
  }
</script>

<svelte:head>
  <title>AT | Cron schedules</title>
</svelte:head>

<div class="p-4 sm:p-6 max-w-6xl mx-auto">
  <LoadIssues issues={pageLoad.issues} retry={load} {loading} />
  <!-- Header -->
  <div class="flex items-center justify-between mb-3">
    <div class="flex items-center gap-2">
      <Clock size={16} class="text-dark-text-muted" />
      <h2 class="text-sm font-medium text-dark-text">Cron schedules</h2>
      <span class="text-xs text-dark-text-muted">({triggers.length})</span>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={() => (tab === 'runs' ? loadRuns() : load())}
        class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary"
        title="Refresh"
      >
        <RefreshCw size={14} />
      </button>
      {#if tab === 'schedules'}
        <button
          onclick={openCreate}
          class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium text-dark-base bg-accent hover:bg-accent-hover"
        >
          <Plus size={12} />
          New Cron Job
        </button>
      {/if}
    </div>
  </div>

  <!-- Tabs -->
  <div class="flex border-b border-dark-border mb-4 text-xs" role="tablist">
    <button
      role="tab"
      aria-selected={tab === 'schedules'}
      onclick={() => (tab = 'schedules')}
      class={["flex items-center gap-1.5 px-3 py-2 -mb-px border-b-2", tab === 'schedules' ? 'border-accent text-dark-text' : 'border-transparent text-dark-text-muted hover:text-dark-text-secondary']}
    >
      <Clock size={12} /> Schedules
    </button>
    <button
      role="tab"
      aria-selected={tab === 'runs'}
      onclick={() => (tab = 'runs')}
      class={["flex items-center gap-1.5 px-3 py-2 -mb-px border-b-2", tab === 'runs' ? 'border-accent text-dark-text' : 'border-transparent text-dark-text-muted hover:text-dark-text-secondary']}
    >
      <History size={12} /> Runs
    </button>
  </div>

  {#if tab === 'runs'}
    {@render runsView()}
  {:else}

  <!-- Form -->
  {#if showForm}
    <div class="border border-dark-border mb-6 bg-dark-surface overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-dark-border">
        <span class="text-sm font-medium text-dark-text">
          {editingId ? 'Edit Cron Job' : 'New Cron Job'}
        </span>
        <button onclick={resetForm} class="p-1 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text-secondary">
          <X size={14} />
        </button>
      </div>

      <form novalidate onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
        <!-- Target Type -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-target-type" class="text-sm font-medium text-dark-text-secondary">Target Type</label>
          <select
            id="form-target-type"
            bind:value={formTargetType}
            onchange={() => { formTargetId = ''; formEntryNodeId = ''; inputNodes = []; }}
            class="col-span-3 border border-dark-border-subtle px-3 py-1.5 text-sm bg-dark-elevated text-dark-text"
          >
            <option value="workflow">Workflow</option>
            <option value="organization">Organization task</option>
          </select>
        </div>

        <!-- Target -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-target" class="text-sm font-medium text-dark-text-secondary">Target</label>
          <select
            id="form-target"
            value={formTargetId}
            onchange={(e) => handleTargetIdChange((e.target as HTMLSelectElement).value)}
            class="col-span-3 border border-dark-border-subtle px-3 py-1.5 text-sm bg-dark-elevated text-dark-text"
          >
            <option value="">Select target...</option>
            {#if formTargetType === 'organization'}
              {#each organizations as o}
                <option value={o.id}>{o.name}</option>
              {/each}
            {:else}
              {#each workflows as w}
                <option value={w.id}>{w.name}</option>
              {/each}
            {/if}
          </select>
        </div>

        {#if formTargetType === 'organization'}
          <p class="text-xs text-dark-text-muted">
            Each run opens one task for the organization's head agent, like a Telegram /new. The run date is added to the title.
          </p>
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-task-title" class="text-sm font-medium text-dark-text-secondary">Task title</label>
            <input
              id="form-task-title"
              type="text"
              bind:value={formTaskTitle}
              placeholder="Daily YouTube Short"
              class="col-span-3 border border-dark-border-subtle px-3 py-1.5 text-sm bg-dark-elevated text-dark-text placeholder:text-dark-text-muted"
            />
          </div>
          <div class="grid grid-cols-4 gap-3 items-start">
            <label for="form-task-desc" class="text-sm font-medium text-dark-text-secondary pt-1.5">Task brief</label>
            <textarea
              id="form-task-desc"
              bind:value={formTaskDescription}
              rows={8}
              placeholder="What the head agent should do on every run"
              class="col-span-3 border border-dark-border-subtle px-3 py-1.5 text-sm bg-dark-elevated text-dark-text placeholder:text-dark-text-muted resize-y"
            ></textarea>
          </div>
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-task-iter" class="text-sm font-medium text-dark-text-secondary">Max iterations</label>
            <input
              id="form-task-iter"
              type="number"
              min="0"
              bind:value={formMaxIterations}
              class="col-span-3 border border-dark-border-subtle px-3 py-1.5 text-sm font-mono bg-dark-elevated text-dark-text"
            />
          </div>
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-notify-bot" class="text-sm font-medium text-dark-text-secondary">Notify on Telegram</label>
            <div class="col-span-3 grid grid-cols-2 gap-2">
              <select
                id="form-notify-bot"
                bind:value={formNotifyBotId}
                onchange={() => (formNotifyChatId = '')}
                class="border border-dark-border-subtle px-3 py-1.5 text-sm bg-dark-elevated text-dark-text"
              >
                <option value="">No notifications</option>
                {#each telegramBots as b}
                  <option value={b.id}>{b.name || b.id}</option>
                {/each}
              </select>
              <select
                bind:value={formNotifyChatId}
                disabled={!formNotifyBotId}
                aria-label="Telegram chat"
                class="border border-dark-border-subtle px-3 py-1.5 text-sm bg-dark-elevated text-dark-text disabled:opacity-50"
              >
                <option value="">Select allowed user...</option>
                {#each notifyUsers as u}
                  <option value={u}>{u}</option>
                {/each}
              </select>
            </div>
          </div>
          <p class="text-xs text-dark-text-muted">
            That chat receives the start message, progress notifications and the result. /status, /result and /resume work there as for a /new task.
          </p>
        {/if}

        <!-- Entry Node (only for workflow) -->
        {#if formTargetType === 'workflow' && inputNodes.length > 1}
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-entry-node" class="text-sm font-medium text-dark-text-secondary">Entry Node</label>
            <div class="col-span-3">
              <select
                id="form-entry-node"
                bind:value={formEntryNodeId}
                class="w-full border border-dark-border-subtle px-3 py-1.5 text-sm bg-dark-elevated text-dark-text"
              >
                <option value="">All input nodes (default)</option>
                {#each inputNodes as node}
                  <option value={node.id}>{node.data?.label || 'Input'} ({node.id.slice(0, 8)}...)</option>
                {/each}
              </select>
            </div>
          </div>
        {/if}

        <!-- Schedule -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-schedule" class="text-sm font-medium text-dark-text-secondary">Schedule</label>
          <div class="col-span-3">
            <input
              id="form-schedule"
              type="text"
              bind:value={formSchedule}
              placeholder="*/5 * * * *"
              class="w-full border border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle bg-dark-elevated text-dark-text placeholder:text-dark-text-muted"
            />
            <div class="flex flex-wrap gap-1 mt-1.5">
              {#each scheduleExamples as ex}
                <button
                  type="button"
                  onclick={() => (formSchedule = ex.value)}
                  class="px-1.5 py-0.5 text-[10px] border border-dark-border-subtle hover:bg-dark-elevated text-dark-text-muted"
                >
                  {ex.label}
                </button>
              {/each}
            </div>
          </div>
        </div>

        <!-- Timezone -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-timezone" class="text-sm font-medium text-dark-text-secondary">Timezone</label>
          <div class="col-span-3">
            <input
              id="form-timezone"
              type="text"
              bind:value={formTimezone}
              placeholder="UTC (default), America/New_York, Europe/London..."
              class="w-full border border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle bg-dark-elevated text-dark-text placeholder:text-dark-text-muted"
            />
          </div>
        </div>

        <!-- Payload -->
        {#if formTargetType === 'workflow'}
          <div class="grid grid-cols-4 gap-3 items-start">
            <label for="form-payload" class="text-sm font-medium text-dark-text-secondary pt-1.5">Payload</label>
            <div class="col-span-3">
              <textarea
                id="form-payload"
                bind:value={formPayload}
                rows={3}
                placeholder={'{}'}
                class="w-full border border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle bg-dark-elevated text-dark-text placeholder:text-dark-text-muted resize-y"
              ></textarea>
              <div class="text-xs text-dark-text-muted mt-1">
                Static JSON data passed as workflow inputs alongside trigger metadata.
              </div>
            </div>
          </div>
        {/if}

        <!-- Enabled -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <span class="text-sm font-medium text-dark-text-secondary">Enabled</span>
          <div class="col-span-3 flex items-center gap-3">
            <label class="relative inline-flex items-center cursor-pointer">
              <input type="checkbox" bind:checked={formEnabled} class="sr-only peer" />
              <div class="w-9 h-5 peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-accent/20 rounded-full peer bg-dark-elevated peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-dark-border after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-dark-surface after:border after:rounded-full after:h-4 after:w-4 after:border-dark-border-subtle peer-checked:bg-accent"></div>
            </label>
          </div>
        </div>

        <!-- Actions -->
        <div class="flex justify-end gap-2 pt-3 border-t border-dark-border">
          <button type="button" onclick={resetForm} class="px-3 py-1.5 text-sm border border-dark-border-subtle hover:bg-dark-elevated text-dark-text-secondary">
            Cancel
          </button>
          <button type="submit" disabled={saving} class="flex items-center gap-1.5 px-3 py-1.5 text-sm text-dark-base bg-accent hover:bg-accent-hover disabled:opacity-50">
            <Save size={14} />
            {saving ? 'Saving...' : editingId ? 'Update' : 'Create'}
          </button>
        </div>
      </form>

      {#if editingId}
        <div class="px-4 pb-4">
          <ExecutionBinding kind="trigger" subjectId={editingId} />
        </div>
      {/if}
    </div>
  {/if}

  <!-- List -->
  {#if pageLoad.loading('Schedules')}
    <div class="text-center py-12 text-dark-text-muted text-sm">Loading cron jobs...</div>
  {:else if pageLoad.error('Schedules') && !triggers.length}
    <p class="text-sm text-dark-text-secondary">Schedules could not be loaded. Retry above.</p>
  {:else if triggers.length === 0}
    <div class="text-center py-12 border border-dark-border">
      <Clock size={24} class="mx-auto mb-2 text-dark-text-muted" />
      <p class="text-sm text-dark-text-muted">No cron jobs configured</p>
      <p class="text-xs text-dark-text-muted mt-1">Create a cron job to run workflows on a schedule</p>
    </div>
  {:else}
    <div class="border border-dark-border overflow-hidden">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-dark-border">
            <th class="text-left px-4 py-2.5 font-medium text-dark-text-muted text-xs">Schedule</th>
            <th class="text-left px-4 py-2.5 font-medium text-dark-text-muted text-xs">Target</th>
            <th class="text-left px-4 py-2.5 font-medium text-dark-text-muted text-xs">Timezone</th>
            <th class="text-left px-4 py-2.5 font-medium text-dark-text-muted text-xs">Status</th>
            <th class="text-right px-4 py-2.5 font-medium text-dark-text-muted text-xs w-44"></th>
          </tr>
        </thead>
        <tbody>
          {#each triggers as t}
            <tr class="border-b border-dark-border last:border-b-0 hover:bg-dark-elevated/50">
              <td class="px-4 py-2.5">
                <div class="font-mono text-xs text-dark-text">{t.config?.schedule || '—'}</div>
                <div class="text-[10px] text-dark-text-muted">{describeSchedule(t.config?.schedule as string || '')}</div>
              </td>
              <td class="px-4 py-2.5">
                <span class="text-xs px-1.5 py-0.5 bg-dark-elevated text-dark-text-secondary">
                  {t.target_type === 'organization' ? 'Organization' : 'Workflow'}
                </span>
                <span class="ml-1.5 text-xs text-dark-text-secondary">{getTargetName(t)}</span>
              </td>
              <td class="px-4 py-2.5 text-xs text-dark-text-muted font-mono">
                {t.config?.timezone || 'UTC'}
              </td>
              <td class="px-4 py-2.5">
                <button
                  onclick={() => toggleEnabled(t)}
                  class="inline-flex items-center gap-1 text-xs"
                  class:text-green-400={t.enabled}
                  class:text-dark-text-muted={!t.enabled}
                  title={t.enabled ? 'Click to disable' : 'Click to enable'}
                >
                  {#if t.enabled}
                    <Power size={12} />
                    Active
                  {:else}
                    <PowerOff size={12} />
                    Disabled
                  {/if}
                </button>
              </td>
              <td class="px-4 py-2.5 text-right">
                <div class="flex justify-end gap-1">
                  <button
                    onclick={() => runNow(t)}
                    disabled={starting === t.id}
                    class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-oc-green disabled:opacity-50"
                    title="Run now"
                    aria-label="Run now"
                  >
                    <Play size={14} />
                  </button>
                  <button
                    onclick={() => { runsTriggerId = t.id; tab = 'runs'; }}
                    class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text"
                    title="Run history"
                    aria-label="Run history"
                  >
                    <History size={14} />
                  </button>
                  <button
                    onclick={() => openEdit(t)}
                    class="p-1.5 hover:bg-dark-elevated text-dark-text-muted hover:text-dark-text"
                    title="Edit"
                  >
                    <Pencil size={14} />
                  </button>
                  {#if deleteConfirm === t.id}
                    <button onclick={() => handleDelete(t.id)} class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700">Confirm</button>
                    <button onclick={() => (deleteConfirm = null)} class="px-2 py-1 text-xs border border-dark-border-subtle hover:bg-dark-elevated">Cancel</button>
                  {:else}
                    <button
                      onclick={() => (deleteConfirm = t.id)}
                      class="p-1.5 hover:bg-red-900/20 text-dark-text-muted hover:text-red-400"
                      title="Delete"
                    >
                      <Trash2 size={14} />
                    </button>
                  {/if}
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
  {/if}
</div>

{#snippet runsView()}
  <div class="flex flex-wrap items-center gap-2 mb-3">
    <label for="runs-trigger" class="text-xs text-dark-text-muted">Schedule</label>
    <select
      id="runs-trigger"
      bind:value={runsTriggerId}
      class="min-w-0 flex-1 sm:flex-none sm:w-96 border border-dark-border-subtle px-3 py-1.5 text-xs bg-dark-elevated text-dark-text"
    >
      {#each triggers as t}
        <option value={t.id}>{triggerLabel(t)}</option>
      {/each}
    </select>
    {#if runsTriggerId}
      {@const current = triggers.find((t) => t.id === runsTriggerId)}
      {#if current}
        <button
          onclick={() => runNow(current)}
          disabled={starting === current.id}
          class="flex items-center gap-1.5 px-3 py-1.5 text-xs border border-dark-border-subtle hover:bg-dark-elevated text-dark-text-secondary disabled:opacity-50"
        >
          <Play size={12} /> Run now
        </button>
      {/if}
    {/if}
  </div>

  {#if !triggers.length}
    <div class="text-center py-12 border border-dark-border text-sm text-dark-text-muted">No cron jobs configured</div>
  {:else if runsError && !runs.length}
    <div class="border border-dark-border p-4 text-sm text-dark-text-secondary flex items-center justify-between">
      <span>{runsError}</span>
      <button onclick={() => loadRuns()} class="px-2 py-1 text-xs border border-dark-border-subtle hover:bg-dark-elevated">Retry</button>
    </div>
  {:else if runsLoading && !runs.length}
    <div class="text-center py-12 text-dark-text-muted text-sm">Loading runs...</div>
  {:else if !runs.length}
    <div class="text-center py-12 border border-dark-border">
      <History size={24} class="mx-auto mb-2 text-dark-text-muted" />
      <p class="text-sm text-dark-text-muted">No runs yet</p>
      <p class="text-xs text-dark-text-muted mt-1">Runs appear here when the schedule fires or when you press Run now.</p>
    </div>
  {:else}
    <div class="border border-dark-border overflow-x-auto">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-dark-border">
            <th class="w-6"></th>
            <th class="text-left px-3 py-2.5 font-medium text-dark-text-muted text-xs">Started</th>
            <th class="text-left px-3 py-2.5 font-medium text-dark-text-muted text-xs">Source</th>
            <th class="text-left px-3 py-2.5 font-medium text-dark-text-muted text-xs">Status</th>
            <th class="text-left px-3 py-2.5 font-medium text-dark-text-muted text-xs">Agent report</th>
            <th class="text-left px-3 py-2.5 font-medium text-dark-text-muted text-xs">Task</th>
            <th class="text-right px-3 py-2.5 font-medium text-dark-text-muted text-xs">Duration</th>
          </tr>
        </thead>
        <tbody>
          {#each runs as r (r.id)}
            <tr
              class="border-b border-dark-border last:border-b-0 hover:bg-dark-surface cursor-pointer"
              onclick={() => toggleRun(r.id)}
            >
              <td class="pl-3 text-dark-text-muted">
                {#if r.id in expanded}<ChevronDown size={12} />{:else}<ChevronRight size={12} />{/if}
              </td>
              <td class="px-3 py-2 text-xs text-dark-text whitespace-nowrap">{formatDateTime(r.started_at)}</td>
              <td class="px-3 py-2 text-xs text-dark-text-secondary">
                {r.source === 'manual' ? 'Manual' : 'Schedule'}
                {#if r.triggered_by}<span class="text-dark-text-muted"> · {r.triggered_by}</span>{/if}
              </td>
              <td class={["px-3 py-2 text-xs", statusClass[r.status] || 'text-dark-text-secondary']}>{r.status}</td>
              <td class="px-3 py-2 text-xs max-w-xs">
                {#if r.reported_status}
                  <span class={reportClass[r.reported_status]}>{r.reported_status}</span>
                  {#if r.reported_summary}<span class="text-dark-text-muted"> · {r.reported_summary.length > 80 ? r.reported_summary.slice(0, 80) + '…' : r.reported_summary}</span>{/if}
                {:else}
                  <span class="text-dark-text-faint">—</span>
                {/if}
              </td>
              <td class="px-3 py-2 text-xs">
                {#if r.task_id}
                  <a href={`#/tasks/${r.task_id}`} onclick={(e) => e.stopPropagation()} class="text-accent hover:underline">{r.task_identifier || r.task_id.slice(0, 8)}</a>
                  {#if r.task_status}<span class="text-dark-text-muted"> · {r.task_status}</span>{/if}
                {:else if r.workflow_run_id}
                  <a href={`#/workflows/${r.target_id}`} onclick={(e) => e.stopPropagation()} class="text-accent hover:underline">workflow</a>
                {:else}
                  <span class="text-dark-text-faint">—</span>
                {/if}
              </td>
              <td class="px-3 py-2 text-xs text-dark-text-muted text-right whitespace-nowrap">{duration(r)}</td>
            </tr>
            {#if r.id in expanded}
              <tr class="border-b border-dark-border last:border-b-0">
                <td colspan="7" class="px-4 py-3">
                  {#if !expanded[r.id]}
                    <p class="text-xs text-dark-text-muted">Loading log...</p>
                  {:else}
                    {@const detail = expanded[r.id]!}
                    {#if detail.reported_summary}
                      <div class="mb-3 text-xs">
                        <span class="text-dark-text-muted">Agent's final report: </span>
                        <span class={reportClass[detail.reported_status] || ''}>{detail.reported_status}</span>
                        <p class="mt-1 text-dark-text whitespace-pre-wrap break-words">{detail.reported_summary}</p>
                      </div>
                    {:else if detail.status !== 'running'}
                      <p class="mb-3 text-xs text-dark-text-muted">The agent did not report an outcome with run_log; judge the result from the status and the log below.</p>
                    {/if}
                    {#if detail.logs?.length}
                      <ol class="space-y-1 font-mono text-xs">
                        {#each detail.logs as l (l.id)}
                          <li class="grid grid-cols-[4.5rem_5.5rem_minmax(0,1fr)] gap-2">
                            <span class="text-dark-text-faint">{new Date(l.created_at).toLocaleTimeString()}</span>
                            <span class={levelClass[l.level]}>{l.level}</span>
                            <span class={["whitespace-pre-wrap break-words", levelClass[l.level]]}>{l.message}</span>
                          </li>
                        {/each}
                      </ol>
                    {:else}
                      <p class="text-xs text-dark-text-muted">No log entries.</p>
                    {/if}
                    {#if detail.result && detail.status !== 'running'}
                      <details class="mt-3 text-xs">
                        <summary class="cursor-pointer text-dark-text-muted">Task result</summary>
                        <pre class="mt-1 whitespace-pre-wrap break-words text-dark-text-secondary max-h-80 overflow-auto">{detail.result}</pre>
                      </details>
                    {/if}
                  {/if}
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>
    {#if runsHasMore}
      <div class="mt-3 text-center">
        <button onclick={() => loadRuns(true)} disabled={runsLoading} class="px-3 py-1.5 text-xs border border-dark-border-subtle hover:bg-dark-elevated text-dark-text-secondary disabled:opacity-50">
          Load older runs
        </button>
      </div>
    {/if}
  {/if}
{/snippet}
