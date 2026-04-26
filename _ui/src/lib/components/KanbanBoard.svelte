<script lang="ts">
  import { dndzone } from 'svelte-dnd-action';
  import { flip } from 'svelte/animate';
  import { push } from 'svelte-spa-router';
  import type { Task } from '@/lib/api/tasks';
  import type { Organization } from '@/lib/api/organizations';
  import type { Agent } from '@/lib/api/agents';
  import { TASK_STATUS_LABELS } from '@/lib/api/tasks';
  import { updateTask } from '@/lib/api/tasks';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    AlertTriangle,
    ArrowUp,
    ArrowDown,
    Minus,
    Circle,
    Building2,
    Play,
    CheckCircle2,
    FileText,
  } from 'lucide-svelte';

  interface Props {
    tasks: Task[];
    organizations?: Organization[];
    agents?: Agent[];
    onStatusChange?: (taskId: string, newStatus: string) => void;
    onProcess?: (task: Task) => void;
  }

  let { tasks, organizations = [], agents = [], onStatusChange, onProcess }: Props = $props();

  function orgName(id: string): string {
    if (!id || !organizations.length) return '';
    const org = organizations.find(o => o.id === id);
    return org?.name || id.substring(0, 12);
  }

  function agentName(id: string): string {
    if (!id) return '';
    const agent = agents.find(a => a.id === id);
    return agent?.name || id.substring(0, 12);
  }

  // ─── 3-Column Layout ───
  // Merged statuses: To Do (backlog+todo+open), In Progress (in_progress+in_review+review), Done (done+completed+blocked+cancelled)

  const columns = [
    { id: 'todo', label: 'To Do', color: 'bg-blue-400', defaultStatus: 'todo' },
    { id: 'in_progress', label: 'In Progress', color: 'bg-yellow-400', defaultStatus: 'in_progress' },
    { id: 'done', label: 'Done', color: 'bg-green-400', defaultStatus: 'done' },
  ] as const;

  // Map any status to one of the 3 columns
  function mapStatusToColumn(status: string): string {
    switch (status) {
      case 'backlog':
      case 'todo':
      case 'open':
        return 'todo';
      case 'in_progress':
      case 'in_review':
      case 'review':
        return 'in_progress';
      case 'done':
      case 'completed':
      case 'blocked':
      case 'cancelled':
        return 'done';
      default:
        return 'todo';
    }
  }

  // Build column items from tasks - each item needs a unique `id` for dnd
  interface DndItem {
    id: string;
    task: Task;
  }

  // Reactive column data
  let columnData = $state<Record<string, DndItem[]>>({});

  $effect(() => {
    const data: Record<string, DndItem[]> = {};
    for (const col of columns) {
      data[col.id] = [];
    }
    for (const task of tasks) {
      const colId = mapStatusToColumn(task.status);
      if (data[colId]) {
        data[colId].push({ id: task.id, task });
      } else {
        data['todo'].push({ id: task.id, task });
      }
    }
    // Sort "done" column descending by updated_at (newest first)
    data['done'].sort((a, b) => (b.task.updated_at || '').localeCompare(a.task.updated_at || ''));
    columnData = data;
  });

  // Handle DnD events
  function handleDndConsider(colId: string, e: CustomEvent<{ items: DndItem[] }>) {
    columnData[colId] = e.detail.items;
  }

  async function handleDndFinalize(colId: string, e: CustomEvent<{ items: DndItem[] }>) {
    columnData[colId] = e.detail.items;

    // Find the column definition to get the default status for drops
    const colDef = columns.find(c => c.id === colId);
    const newStatus = colDef?.defaultStatus || colId;

    // Find task that moved to this column and update its status
    for (const item of e.detail.items) {
      const currentMapped = mapStatusToColumn(item.task.status);
      if (currentMapped !== colId) {
        try {
          await updateTask(item.task.id, { status: newStatus });
          item.task.status = newStatus;
          if (onStatusChange) onStatusChange(item.task.id, newStatus);
        } catch (err: any) {
          addToast(err?.response?.data?.message || 'Failed to update task status', 'alert');
        }
      }
    }
  }

  // Context menu
  let contextMenu = $state<{ x: number; y: number; task: Task } | null>(null);

  function openContextMenu(e: MouseEvent, task: Task) {
    if (!task.organization_id) return;
    e.preventDefault();
    e.stopPropagation();
    contextMenu = { x: e.clientX, y: e.clientY, task };
  }

  function closeContextMenu() {
    contextMenu = null;
  }

  function handleContextProcess() {
    if (contextMenu && onProcess) {
      onProcess(contextMenu.task);
    }
    closeContextMenu();
  }

  // ─── Card Helpers ───

  // Priority icon helper
  function priorityIcon(level: string) {
    switch (level) {
      case 'critical': return AlertTriangle;
      case 'high': return ArrowUp;
      case 'medium': return Minus;
      case 'low': return ArrowDown;
      default: return null;
    }
  }

  function priorityColor(level: string): string {
    switch (level) {
      case 'critical': return 'text-red-500';
      case 'high': return 'text-orange-500';
      case 'medium': return 'text-yellow-500';
      case 'low': return 'text-blue-400';
      default: return 'text-gray-400';
    }
  }

  // Status badge colors (for distinguishing merged statuses within a column)
  function statusBadgeClasses(status: string): string {
    switch (status) {
      case 'backlog':
        return 'bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted';
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
        return 'bg-gray-100 dark:bg-dark-elevated text-gray-400 dark:text-dark-text-faint';
      default:
        return 'bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted';
    }
  }

  // Left border stripe color per status
  function statusStripeColor(status: string): string {
    switch (status) {
      case 'backlog':
        return 'border-l-gray-300 dark:border-l-gray-600';
      case 'open':
      case 'todo':
        return 'border-l-blue-400 dark:border-l-blue-500';
      case 'in_progress':
        return 'border-l-yellow-400 dark:border-l-yellow-500';
      case 'in_review':
      case 'review':
        return 'border-l-purple-400 dark:border-l-purple-500';
      case 'blocked':
        return 'border-l-red-400 dark:border-l-red-500';
      case 'completed':
      case 'done':
        return 'border-l-green-400 dark:border-l-green-500';
      case 'cancelled':
        return 'border-l-gray-300 dark:border-l-gray-600';
      default:
        return 'border-l-gray-300 dark:border-l-gray-600';
    }
  }

  // Truncate description for preview
  function descriptionPreview(desc: string | undefined): string {
    if (!desc) return '';
    const clean = desc.replace(/\n/g, ' ').trim();
    return clean.length > 80 ? clean.substring(0, 80) + '...' : clean;
  }

  // Check if a task is in a "failed" state (blocked/cancelled) — visually distinct in Done column
  function isFailedStatus(status: string): boolean {
    return status === 'blocked' || status === 'cancelled';
  }
</script>

<div class="flex gap-4 overflow-x-auto pb-4 h-full min-h-0">
  {#each columns as col}
    <div class="flex flex-col min-w-[300px] flex-1 bg-gray-50 dark:bg-dark-base border border-gray-200 dark:border-dark-border">
      <!-- Column header -->
      <div class="flex items-center gap-2 px-3 py-2.5 border-b border-gray-200 dark:border-dark-border">
        <div class="w-2.5 h-2.5 {col.color}"></div>
        <span class="text-xs font-semibold text-gray-700 dark:text-dark-text-secondary uppercase tracking-wider">{col.label}</span>
        <span class="text-xs text-gray-400 dark:text-dark-text-muted ml-auto font-mono">{columnData[col.id]?.length || 0}</span>
      </div>

      <!-- Droppable zone -->
      <div
        class="flex-1 overflow-y-auto p-2.5 space-y-2.5 min-h-[100px]"
        use:dndzone={{ items: columnData[col.id] || [], flipDurationMs: 200, dropTargetStyle: {} }}
        onconsider={(e) => handleDndConsider(col.id, e)}
        onfinalize={(e) => handleDndFinalize(col.id, e)}
      >
        {#each columnData[col.id] || [] as item (item.id)}
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <div
            animate:flip={{ duration: 200 }}
            class={[
              'bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border border-l-3 p-3.5 hover:border-gray-300 dark:hover:border-dark-border-subtle hover:shadow-sm transition-all',
              statusStripeColor(item.task.status),
              isFailedStatus(item.task.status) ? 'opacity-70' : '',
              'cursor-grab active:cursor-grabbing',
            ]}
            oncontextmenu={(e) => openContextMenu(e, item.task)}
          >
            <!-- Card top row: identifier + priority + status badge -->
            <div class="flex items-center justify-between mb-1.5">
              <div class="flex items-center gap-2">
                {#if item.task.identifier}
                  <span class="text-[10px] font-mono text-gray-400 dark:text-dark-text-muted">{item.task.identifier}</span>
                {:else}
                  <span class="text-[10px] font-mono text-gray-300 dark:text-dark-text-faint">{item.task.id.slice(0, 8)}</span>
                {/if}
                <span class="inline-block px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wide {statusBadgeClasses(item.task.status)}">
                  {TASK_STATUS_LABELS[item.task.status] || item.task.status.replace(/_/g, ' ')}
                </span>
              </div>
              {#if item.task.priority_level}
                {@const Icon = priorityIcon(item.task.priority_level)}
                {#if Icon}
                  <Icon size={12} class={priorityColor(item.task.priority_level)} />
                {/if}
              {/if}
            </div>

            <!-- Title (clickable to detail) -->
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <div
              class={[
                'text-sm font-medium leading-snug mb-1.5 cursor-pointer hover:text-gray-600 dark:hover:text-dark-text-secondary',
                item.task.status === 'cancelled'
                  ? 'line-through text-gray-400 dark:text-dark-text-muted'
                  : 'text-gray-900 dark:text-dark-text',
              ]}
              onclick={() => push(`/tasks/${item.task.id}`)}
            >
              {item.task.title || item.task.identifier || item.task.id.slice(0, 12)}
            </div>

            <!-- Description preview -->
            {#if item.task.description}
              {@const preview = descriptionPreview(item.task.description)}
              {#if preview}
                <p class="text-xs text-gray-400 dark:text-dark-text-muted leading-relaxed mb-2 line-clamp-2">
                  {preview}
                </p>
              {/if}
            {/if}

            <!-- Bottom row: agent + org + result indicator -->
            <div class="flex items-center gap-2 flex-wrap">
              {#if item.task.assigned_agent_id}
                <div class="flex items-center gap-1 text-[10px] text-gray-500 dark:text-dark-text-muted">
                  <Circle size={8} />
                  <span class="truncate max-w-[100px]">{agentName(item.task.assigned_agent_id)}</span>
                </div>
              {/if}
              {#if item.task.organization_id}
                <div class="flex items-center gap-1 text-[10px] text-gray-500 dark:text-dark-text-muted">
                  <Building2 size={8} />
                  <span class="truncate max-w-[100px]">{orgName(item.task.organization_id)}</span>
                </div>
              {/if}
              {#if item.task.result}
                <div class="flex items-center gap-1 text-[10px] ml-auto" title="Has result">
                  {#if isFailedStatus(item.task.status)}
                    <FileText size={10} class="text-gray-400 dark:text-dark-text-muted" />
                  {:else}
                    <CheckCircle2 size={10} class="text-green-500 dark:text-green-400" />
                  {/if}
                </div>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    </div>
  {/each}
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
    class="fixed z-50 bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border shadow-lg py-1 min-w-[180px]"
    style="left: {contextMenu.x}px; top: {contextMenu.y}px;"
  >
    <button
      onclick={handleContextProcess}
      class="w-full flex items-center gap-2 px-3 py-2 text-sm text-gray-700 dark:text-dark-text hover:bg-gray-100 dark:hover:bg-dark-elevated transition-colors"
    >
      <Play size={14} class="text-green-500" />
      Start Processing
    </button>
  </div>
{/if}

<svelte:window onkeydown={(e) => { if (e.key === 'Escape') closeContextMenu(); }} />
