<script lang="ts">
  import { dndzone } from 'svelte-dnd-action';
  import { flip } from 'svelte/animate';
  import { push } from 'svelte-spa-router';
  import type { Task } from '@/lib/api/tasks';
  import { TASK_STATUS_LABELS } from '@/lib/api/tasks';
  import { updateTask } from '@/lib/api/tasks';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    AlertTriangle,
    ArrowUp,
    ArrowDown,
    Minus,
    Circle,
    CheckCircle2,
    XCircle,
    Clock,
    Eye,
    Ban,
    Loader,
  } from 'lucide-svelte';

  interface Props {
    tasks: Task[];
    onStatusChange?: (taskId: string, newStatus: string) => void;
  }

  let { tasks, onStatusChange }: Props = $props();

  // Kanban column definitions
  const columns = [
    { id: 'backlog', label: 'Backlog', color: 'bg-gray-400' },
    { id: 'todo', label: 'To Do', color: 'bg-blue-400' },
    { id: 'in_progress', label: 'In Progress', color: 'bg-yellow-400' },
    { id: 'in_review', label: 'In Review', color: 'bg-purple-400' },
    { id: 'blocked', label: 'Blocked', color: 'bg-red-400' },
    { id: 'done', label: 'Done', color: 'bg-green-400' },
    { id: 'cancelled', label: 'Cancelled', color: 'bg-gray-300' },
  ] as const;

  // Legacy status mapping: map old statuses to new columns
  function mapStatusToColumn(status: string): string {
    switch (status) {
      case 'open': return 'todo';
      case 'review': return 'in_review';
      case 'completed': return 'done';
      default: return status;
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
        // Unknown status, put in backlog
        data['backlog'].push({ id: task.id, task });
      }
    }
    columnData = data;
  });

  // Handle DnD events
  function handleDndConsider(colId: string, e: CustomEvent<{ items: DndItem[] }>) {
    columnData[colId] = e.detail.items;
  }

  async function handleDndFinalize(colId: string, e: CustomEvent<{ items: DndItem[] }>) {
    columnData[colId] = e.detail.items;

    // Find task that moved to this column and update its status
    for (const item of e.detail.items) {
      const currentMapped = mapStatusToColumn(item.task.status);
      if (currentMapped !== colId) {
        try {
          await updateTask(item.task.id, { ...item.task, status: colId });
          item.task.status = colId;
          if (onStatusChange) onStatusChange(item.task.id, colId);
        } catch (err: any) {
          addToast(err?.response?.data?.message || 'Failed to update task status', 'alert');
        }
      }
    }
  }

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
</script>

<div class="flex gap-3 overflow-x-auto pb-4 h-full min-h-0">
  {#each columns as col}
    <div class="flex flex-col min-w-[260px] w-[260px] shrink-0 bg-gray-50 dark:bg-dark-base border border-gray-200 dark:border-dark-border rounded">
      <!-- Column header -->
      <div class="flex items-center gap-2 px-3 py-2 border-b border-gray-200 dark:border-dark-border">
        <div class="w-2 h-2 rounded-full {col.color}"></div>
        <span class="text-xs font-medium text-gray-700 dark:text-dark-text-secondary uppercase tracking-wider">{col.label}</span>
        <span class="text-xs text-gray-400 dark:text-dark-text-muted ml-auto">{columnData[col.id]?.length || 0}</span>
      </div>

      <!-- Droppable zone -->
      <div
        class="flex-1 overflow-y-auto p-2 space-y-2 min-h-[100px]"
        use:dndzone={{ items: columnData[col.id] || [], flipDurationMs: 200, dropTargetStyle: {} }}
        onconsider={(e) => handleDndConsider(col.id, e)}
        onfinalize={(e) => handleDndFinalize(col.id, e)}
      >
        {#each columnData[col.id] || [] as item (item.id)}
          <div
            animate:flip={{ duration: 200 }}
            class="bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border rounded p-3 cursor-grab active:cursor-grabbing hover:border-gray-300 dark:hover:border-dark-border-subtle hover:shadow-sm transition-all"
          >
            <!-- Card top: identifier + priority -->
            <div class="flex items-center justify-between mb-1">
              {#if item.task.identifier}
                <span class="text-[10px] font-mono text-gray-400 dark:text-dark-text-muted">{item.task.identifier}</span>
              {:else}
                <span class="text-[10px] font-mono text-gray-300 dark:text-dark-text-faint">{item.task.id.slice(0, 8)}</span>
              {/if}
              {#if item.task.priority_level}
                {@const Icon = priorityIcon(item.task.priority_level)}
                {#if Icon}
                  <svelte:component this={Icon} size={12} class={priorityColor(item.task.priority_level)} />
                {/if}
              {/if}
            </div>

            <!-- Title (clickable to detail) -->
            <!-- svelte-ignore a11y_click_events_have_key_events -->
            <!-- svelte-ignore a11y_no_static_element_interactions -->
            <div
              class="text-sm font-medium text-gray-900 dark:text-dark-text leading-snug mb-2 cursor-pointer hover:text-gray-600 dark:hover:text-dark-text-secondary"
              onclick={() => push(`/tasks/${item.task.id}`)}
            >
              {item.task.title}
            </div>

            <!-- Bottom: assignee -->
            {#if item.task.assigned_agent_id}
              <div class="flex items-center gap-1 text-[10px] text-gray-400 dark:text-dark-text-muted">
                <Circle size={8} />
                <span class="truncate">{item.task.assigned_agent_id}</span>
              </div>
            {/if}
          </div>
        {/each}
      </div>
    </div>
  {/each}
</div>
