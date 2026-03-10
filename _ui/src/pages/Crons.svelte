<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    listAllTriggers,
    createTrigger,
    updateTrigger,
    deleteTrigger,
    type Trigger,
  } from '@/lib/api/triggers';
  import { listWorkflows, getWorkflow, type Workflow, type WorkflowNode } from '@/lib/api/workflows';
  import { listCollections, type RAGCollection } from '@/lib/api/rag';
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
  } from 'lucide-svelte';
  import { formatDate } from '@/lib/helper/format';

  storeNavbar.title = 'Cron Jobs';

  // ─── State ───

  let triggers = $state<Trigger[]>([]);
  let loading = $state(true);

  // Reference data
  let workflows = $state<Workflow[]>([]);
  let ragCollections = $state<RAGCollection[]>([]);

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
    try {
      const [trigs, wfRes, ragRes] = await Promise.all([
        listAllTriggers({ type: 'cron' }),
        listWorkflows({ _limit: 1000 }).catch(() => ({ data: [], meta: { total: 0, offset: 0, limit: 0 } })),
        listCollections({ _limit: 1000 }).catch(() => ({ data: [], meta: { total: 0, offset: 0, limit: 0 } })),
      ]);
      triggers = trigs;
      workflows = wfRes.data || [];
      ragCollections = ragRes.data || [];
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
    if (t.target_type === 'rag_sync') {
      return ragCollections.find(c => c.id === t.target_id)?.name || t.target_id;
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

    let extraPayload: Record<string, any> = {};
    try {
      extraPayload = JSON.parse(formPayload);
    } catch {
      addToast('Payload must be valid JSON', 'warn');
      return;
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

      const payload: Partial<Trigger> = {
        type: 'cron',
        target_type: formTargetType,
        target_id: formTargetId,
        entry_node_id: formEntryNodeId || undefined,
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
</script>

<svelte:head>
  <title>AT | Cron Jobs</title>
</svelte:head>

<div class="p-6 max-w-5xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-4">
    <div class="flex items-center gap-2">
      <Clock size={16} class="text-gray-500 dark:text-dark-text-muted" />
      <h2 class="text-sm font-medium text-gray-900 dark:text-dark-text">Cron Jobs</h2>
      <span class="text-xs text-gray-400 dark:text-dark-text-muted">({triggers.length})</span>
    </div>
    <div class="flex items-center gap-2">
      <button
        onclick={load}
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
        New Cron Job
      </button>
    </div>
  </div>

  <!-- Form -->
  {#if showForm}
    <div class="border border-gray-200 dark:border-dark-border mb-6 bg-white dark:bg-dark-surface overflow-hidden">
      <div class="flex items-center justify-between px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <span class="text-sm font-medium text-gray-900 dark:text-dark-text">
          {editingId ? 'Edit Cron Job' : 'New Cron Job'}
        </span>
        <button onclick={resetForm} class="p-1 hover:bg-gray-200 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors">
          <X size={14} />
        </button>
      </div>

      <form novalidate onsubmit={(e) => { e.preventDefault(); handleSubmit(); }} class="p-4 space-y-4">
        <!-- Target Type -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-target-type" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Target Type</label>
          <select
            id="form-target-type"
            bind:value={formTargetType}
            onchange={() => { formTargetId = ''; formEntryNodeId = ''; inputNodes = []; }}
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm dark:bg-dark-elevated dark:text-dark-text transition-colors"
          >
            <option value="workflow">Workflow</option>
            <option value="rag_sync">RAG Sync</option>
          </select>
        </div>

        <!-- Target -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-target" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Target</label>
          <select
            id="form-target"
            value={formTargetId}
            onchange={(e) => handleTargetIdChange((e.target as HTMLSelectElement).value)}
            class="col-span-3 border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm dark:bg-dark-elevated dark:text-dark-text transition-colors"
          >
            <option value="">Select target...</option>
            {#if formTargetType === 'workflow'}
              {#each workflows as w}
                <option value={w.id}>{w.name}</option>
              {/each}
            {:else}
              {#each ragCollections as c}
                <option value={c.id}>{c.name}</option>
              {/each}
            {/if}
          </select>
        </div>

        <!-- Entry Node (only for workflow) -->
        {#if formTargetType === 'workflow' && inputNodes.length > 1}
          <div class="grid grid-cols-4 gap-3 items-center">
            <label for="form-entry-node" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Entry Node</label>
            <div class="col-span-3">
              <select
                id="form-entry-node"
                bind:value={formEntryNodeId}
                class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm dark:bg-dark-elevated dark:text-dark-text transition-colors"
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
          <label for="form-schedule" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Schedule</label>
          <div class="col-span-3">
            <input
              id="form-schedule"
              type="text"
              bind:value={formSchedule}
              placeholder="*/5 * * * *"
              class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
            />
            <div class="flex flex-wrap gap-1 mt-1.5">
              {#each scheduleExamples as ex}
                <button
                  type="button"
                  onclick={() => (formSchedule = ex.value)}
                  class="px-1.5 py-0.5 text-[10px] border border-gray-200 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-500 dark:text-dark-text-muted transition-colors"
                >
                  {ex.label}
                </button>
              {/each}
            </div>
          </div>
        </div>

        <!-- Timezone -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <label for="form-timezone" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Timezone</label>
          <div class="col-span-3">
            <input
              id="form-timezone"
              type="text"
              bind:value={formTimezone}
              placeholder="UTC (default), America/New_York, Europe/London..."
              class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors"
            />
          </div>
        </div>

        <!-- Payload -->
        {#if formTargetType === 'workflow'}
          <div class="grid grid-cols-4 gap-3 items-start">
            <label for="form-payload" class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary pt-1.5">Payload</label>
            <div class="col-span-3">
              <textarea
                id="form-payload"
                bind:value={formPayload}
                rows={3}
                placeholder={'{}'}
                class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 text-sm font-mono focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 focus:border-gray-400 dark:focus:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text dark:placeholder:text-dark-text-muted transition-colors resize-y"
              ></textarea>
              <div class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">
                Static JSON data passed as workflow inputs alongside trigger metadata.
              </div>
            </div>
          </div>
        {/if}

        <!-- Enabled -->
        <div class="grid grid-cols-4 gap-3 items-center">
          <span class="text-sm font-medium text-gray-700 dark:text-dark-text-secondary">Enabled</span>
          <div class="col-span-3 flex items-center gap-3">
            <label class="relative inline-flex items-center cursor-pointer">
              <input type="checkbox" bind:checked={formEnabled} class="sr-only peer" />
              <div class="w-9 h-5 bg-gray-200 peer-focus:outline-none peer-focus:ring-2 peer-focus:ring-gray-900/10 dark:peer-focus:ring-accent/20 rounded-full peer dark:bg-dark-elevated peer-checked:after:translate-x-full rtl:peer-checked:after:-translate-x-full peer-checked:after:border-white after:content-[''] after:absolute after:top-[2px] after:start-[2px] after:bg-white after:border-gray-300 after:border after:rounded-full after:h-4 after:w-4 after:transition-all dark:after:border-dark-border-subtle peer-checked:bg-gray-900 dark:peer-checked:bg-accent"></div>
            </label>
          </div>
        </div>

        <!-- Actions -->
        <div class="flex justify-end gap-2 pt-3 border-t border-gray-100 dark:border-dark-border">
          <button type="button" onclick={resetForm} class="px-3 py-1.5 text-sm border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary transition-colors">
            Cancel
          </button>
          <button type="submit" disabled={saving} class="flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors disabled:opacity-50">
            <Save size={14} />
            {saving ? 'Saving...' : editingId ? 'Update' : 'Create'}
          </button>
        </div>
      </form>
    </div>
  {/if}

  <!-- List -->
  {#if loading}
    <div class="text-center py-12 text-gray-400 dark:text-dark-text-muted text-sm">Loading cron jobs...</div>
  {:else if triggers.length === 0}
    <div class="text-center py-12 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
      <Clock size={24} class="mx-auto mb-2 text-gray-300 dark:text-dark-text-muted" />
      <p class="text-sm text-gray-500 dark:text-dark-text-muted">No cron jobs configured</p>
      <p class="text-xs text-gray-400 dark:text-dark-text-muted mt-1">Create a cron job to run workflows or RAG syncs on a schedule</p>
    </div>
  {:else}
    <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
      <table class="w-full text-sm">
        <thead>
          <tr class="border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Schedule</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Target</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Timezone</th>
            <th class="text-left px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider">Status</th>
            <th class="text-right px-4 py-2.5 font-medium text-gray-500 dark:text-dark-text-muted text-xs uppercase tracking-wider w-32"></th>
          </tr>
        </thead>
        <tbody>
          {#each triggers as t}
            <tr class="border-b border-gray-100 dark:border-dark-border last:border-b-0 hover:bg-gray-50/50 dark:hover:bg-dark-elevated/50 transition-colors">
              <td class="px-4 py-2.5">
                <div class="font-mono text-xs text-gray-900 dark:text-dark-text">{t.config?.schedule || '—'}</div>
                <div class="text-[10px] text-gray-400 dark:text-dark-text-muted">{describeSchedule(t.config?.schedule as string || '')}</div>
              </td>
              <td class="px-4 py-2.5">
                <span class="text-xs px-1.5 py-0.5 bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-secondary">
                  {t.target_type === 'rag_sync' ? 'RAG' : 'Workflow'}
                </span>
                <span class="ml-1.5 text-xs text-gray-700 dark:text-dark-text-secondary">{getTargetName(t)}</span>
              </td>
              <td class="px-4 py-2.5 text-xs text-gray-500 dark:text-dark-text-muted font-mono">
                {t.config?.timezone || 'UTC'}
              </td>
              <td class="px-4 py-2.5">
                <button
                  onclick={() => toggleEnabled(t)}
                  class="inline-flex items-center gap-1 text-xs transition-colors"
                  class:text-green-600={t.enabled}
                  class:dark:text-green-400={t.enabled}
                  class:text-gray-400={!t.enabled}
                  class:dark:text-dark-text-muted={!t.enabled}
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
                    onclick={() => openEdit(t)}
                    class="p-1.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-colors"
                    title="Edit"
                  >
                    <Pencil size={14} />
                  </button>
                  {#if deleteConfirm === t.id}
                    <button onclick={() => handleDelete(t.id)} class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors">Confirm</button>
                    <button onclick={() => (deleteConfirm = null)} class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors">Cancel</button>
                  {:else}
                    <button
                      onclick={() => (deleteConfirm = t.id)}
                      class="p-1.5 hover:bg-red-50 dark:hover:bg-red-900/20 text-gray-400 dark:text-dark-text-muted hover:text-red-600 dark:hover:text-red-400 transition-colors"
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
</div>
