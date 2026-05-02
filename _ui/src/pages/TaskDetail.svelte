<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { push } from 'svelte-spa-router';
  import {
    getTask,
    updateTask,
    deleteTask,
    getTaskWithSubtasks,
    processTask,
    createTaskChat,
    cancelTaskDelegation,
    listActiveDelegations,
    type ActiveDelegation,
    TASK_STATUSES,
    TASK_STATUS_LABELS,
    TASK_PRIORITIES,
    TASK_PRIORITY_LABELS,
    type Task,
    type TaskWithSubtasks,
  } from '@/lib/api/tasks';
  import {
    listLabelsForTask,
    listLabels,
    createLabel,
    addLabelToTask,
    removeLabelFromTask,
    type Label,
  } from '@/lib/api/labels';
  import { formatDate, formatDateTime } from '@/lib/helper/format';
  import Markdown from '@/lib/components/Markdown.svelte';
  import CommentThread from '@/lib/components/CommentThread.svelte';
  import {
    ArrowLeft, Save, Trash2, Pencil, X, Check,
    Tag, MessageSquare, ListTree, Calendar, User,
    FolderOpen, Hash, Clock, AlertTriangle, CreditCard,
    Layers, ChevronRight, ChevronDown, Building2, Play,
    RotateCcw, RefreshCw, Send, Square, Loader2, Activity,
    Receipt,
  } from 'lucide-svelte';
  import {
    listChatMessages,
    sendMessage as sendChatMessage,
    type ChatMessage,
  } from '@/lib/api/chat-sessions';
  import { createComment } from '@/lib/api/issue-comments';
  import { listOrganizations, type Organization } from '@/lib/api/organizations';
  import { listAgents, type Agent } from '@/lib/api/agents';
  import { getCostByTask, type CostByTaskResult } from '@/lib/api/cost-events';
  import { listAuditEntries, type AuditEntry } from '@/lib/api/audit';

  interface Props {
    params: { id: string };
  }

  let { params }: Props = $props();

  storeNavbar.title = 'Task Detail';

  // ─── State ───

  let task = $state<Task | null>(null);
  let loading = $state(true);
  let saving = $state(false);
  let deleteConfirm = $state(false);

  // Inline editing
  let editingTitle = $state(false);
  let editTitle = $state('');
  let editingDescription = $state(false);
  let editDescription = $state('');

  // Labels
  let taskLabels = $state<Label[]>([]);
  let allLabels = $state<Label[]>([]);
  let showLabelPicker = $state(false);
  let labelsLoading = $state(false);
  let newLabelName = $state('');
  let newLabelColor = $state('#3b82f6');
  let creatingLabel = $state(false);

  const LABEL_COLOR_PRESETS = [
    '#ef4444', '#f97316', '#eab308', '#22c55e', '#14b8a6',
    '#3b82f6', '#6366f1', '#a855f7', '#ec4899', '#6b7280',
  ];

  // Sub-tasks (delegation tree)
  let taskTree = $state<TaskWithSubtasks | null>(null);
  let subTasksLoading = $state(false);
  let expandedNodes = $state<Set<string>>(new Set());

  // Rolled-up cost (this task + all descendants). Loaded lazily on demand
  // because the BFS traversal touches every sub-task row in the database;
  // we don't want to pay it on every TaskDetail open. Refreshed manually.
  let costRollup = $state<CostByTaskResult | null>(null);
  let costLoading = $state(false);

  async function loadCost() {
    if (!task) return;
    costLoading = true;
    try {
      costRollup = await getCostByTask(task.id);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load cost rollup', 'alert');
    } finally {
      costLoading = false;
    }
  }

  // Format cents → "$0.0042" (matches CostEvents page convention).
  function formatCostCents(cents: number): string {
    return `$${(cents / 100).toFixed(4)}`;
  }

  function formatTokensShort(n: number): string {
    if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
    if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
    return String(n);
  }

  // URL for the Cost Events page filtered to the task tree. The CostEvents
  // page reads ?task_ids=A,B,C from the hash querystring and passes it
  // through to the backend as task_id[in]=A,B,C.
  let costEventsUrl = $derived.by(() => {
    if (!costRollup || !costRollup.task_ids?.length) return '#/cost-events';
    return `#/cost-events?task_ids=${encodeURIComponent(costRollup.task_ids.join(','))}`;
  });

  // Active tab
  let activeTab = $state<'comments' | 'subtasks' | 'labels' | 'activity' | 'events'>('activity');

  // ─── Events feed (audit-driven live timeline) ───
  // Pulls audit entries for this task and every descendant task in the
  // delegation tree. Refreshed on subtask reload (which already happens
  // every 12s while a delegation is running) and on demand. This is the
  // "show me what's happening" view that lets the user follow Content
  // Director → Script Writer → Video Producer in real time.
  let events = $state<AuditEntry[]>([]);
  let eventsLoading = $state(false);

  async function loadEvents() {
    if (!task) return;
    eventsLoading = true;
    try {
      const ids = collectTreeTaskIds(taskTree);
      if (!ids.includes(task.id)) ids.push(task.id);
      // Fetch the most recent N entries for the whole tree. The store
      // accepts `resource_id[in]=A,B,C` via the query.Parse filter syntax.
      const params: Record<string, string | number> = {
        _limit: 200,
        _order: 'created_at',
        _orderdir: 'desc',
        'resource_id[in]': ids.join(','),
        'resource_type[in]': 'task,tool',
      };
      const res = await listAuditEntries(params as any);
      events = res.data || [];
    } catch {
      events = [];
    } finally {
      eventsLoading = false;
    }
  }

  // Refresh events when the events tab is opened, when the subtask
  // tree changes, and when a delegation finishes.
  $effect(() => {
    if (activeTab === 'events' && task) {
      loadEvents();
    }
  });

  // Render-friendly view of an audit entry. Maps action codes to short
  // human-readable strings and surfaces the actor agent name.
  function eventLabel(e: AuditEntry): string {
    switch (e.action) {
      case 'task_started':   return 'started task';
      case 'task_completed': return 'completed task';
      case 'task_delegated': return 'delegated to subtask';
      case 'task_failed':    return 'task failed';
      case 'task_cancelled': return 'task cancelled';
      case 'tool_call': {
        const tool = e.details?.tool_name || 'tool';
        // Pretty-print delegate_to_<agent> as "delegated to <Agent>".
        if (typeof tool === 'string' && tool.startsWith('delegate_to_')) {
          const target = tool.slice('delegate_to_'.length).replace(/_/g, ' ');
          return `delegated to ${target}`;
        }
        return `called ${tool}`;
      }
      case 'llm_call': {
        const model = e.details?.model;
        return model ? `LLM call (${model})` : 'LLM call';
      }
      default: return e.action;
    }
  }

  function eventActor(e: AuditEntry): string {
    if (e.actor_type === 'agent') return agentDisplayName(e.actor_id);
    if (e.actor_type === 'user') return e.actor_id || 'user';
    return e.actor_id || e.actor_type;
  }

  function eventTaskTitle(e: AuditEntry): string {
    if (e.resource_type !== 'task') return '';
    // Find the matching node in the tree.
    function find(n: TaskWithSubtasks | null): TaskWithSubtasks | null {
      if (!n) return null;
      if (n.id === e.resource_id) return n;
      for (const c of n.sub_tasks ?? []) {
        const f = find(c);
        if (f) return f;
      }
      return null;
    }
    const node = find(taskTree);
    return node?.title || '';
  }

  // ─── Activity / Chat state ───
  let chatSessionId = $state<string | null>(null);
  let chatMessages = $state<ChatMessage[]>([]);
  let chatLoading = $state(false);
  let chatInput = $state('');
  let chatSending = $state(false);
  let chatStreamContent = $state('');
  let chatToolEvents = $state<{ type: string; name: string; id?: string; result?: string }[]>([]);
  let chatExpandedTools = $state<Record<string, boolean>>({});
  let chatAbortController: AbortController | null = null;
  let chatMessagesEnd = $state<HTMLDivElement | undefined>(undefined);
  let chatInputEl = $state<HTMLTextAreaElement | undefined>(undefined);

  // ─── Active delegation tracking ───
  // Active delegations include the root task AND any in-flight descendant
  // task (delegated child / grandchild / ...). We use this to highlight
  // the in-flight node in the subtask tree and to show the user *which*
  // agent is currently working — not just "an agent is working".
  let delegationActive = $state(false);            // any descendant active
  let delegationRootActive = $state(false);        // *this* task active
  let delegationDuration = $state('');             // duration of deepest active
  let activeTaskIds = $state<Set<string>>(new Set()); // all active task_ids in this tree
  let activeDeepest = $state<ActiveDelegation | null>(null); // deepest in-flight delegation (for "which agent is working")
  let cancelling = $state(false);
  let delegationPollTimer: ReturnType<typeof setInterval> | null = null;

  // Reference data
  let organizations = $state<Organization[]>([]);
  let agents = $state<Agent[]>([]);

  function orgName(id: string): string {
    if (!id) return '';
    const org = organizations.find(o => o.id === id);
    return org?.name || id.substring(0, 12);
  }

  function agentDisplayName(id: string): string {
    if (!id) return '-';
    const agent = agents.find(a => a.id === id);
    return agent?.name || id;
  }

  async function loadReferenceData() {
    try {
      const [orgRes, agentRes] = await Promise.all([
        listOrganizations({ _limit: 200 }),
        listAgents({ _limit: 200 }),
      ]);
      organizations = orgRes.data || [];
      agents = agentRes.data || [];
    } catch {
      // Non-fatal
    }
  }

  // ─── Load ───

  async function loadTask() {
    loading = true;
    try {
      task = await getTask(params.id);
      storeNavbar.title = task.title || 'Task Detail';
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load task', 'alert');
      push('/tasks');
    } finally {
      loading = false;
    }
  }

  async function loadLabels() {
    labelsLoading = true;
    try {
      const [tl, al] = await Promise.all([
        listLabelsForTask(params.id),
        listLabels({ _limit: 200 }),
      ]);
      taskLabels = tl || [];
      allLabels = al.data || [];
    } catch {
      // Labels may not be supported; silently ignore
    } finally {
      labelsLoading = false;
    }
  }

  async function loadSubTasks() {
    subTasksLoading = true;
    try {
      taskTree = await getTaskWithSubtasks(params.id);
      // Auto-expand root's direct children
      if (taskTree?.sub_tasks?.length) {
        expandedNodes = new Set([params.id]);
      }
    } catch {
      taskTree = null;
    } finally {
      subTasksLoading = false;
    }
  }

  // Reference data is independent of the task id — fetch once
  loadReferenceData();

  // Load (and reload when params change so navigating between tasks without
  // remounting still works)
  $effect(() => {
    if (params.id) {
      loadTask();
      loadLabels();
      loadSubTasks();
    }
  });

  // ─── Status & Priority helpers ───

  function statusClasses(status: string): string {
    switch (status) {
      case 'backlog': return 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted';
      case 'open':
      case 'todo': return 'bg-blue-100 dark:bg-blue-900/30 text-blue-700 dark:text-blue-400';
      case 'in_progress': return 'bg-yellow-100 dark:bg-yellow-900/30 text-yellow-700 dark:text-yellow-400';
      case 'in_review':
      case 'review': return 'bg-purple-100 dark:bg-purple-900/30 text-purple-700 dark:text-purple-400';
      case 'blocked': return 'bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-400';
      case 'completed':
      case 'done': return 'bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-400';
      case 'cancelled': return 'bg-gray-100 dark:bg-dark-elevated text-gray-500 dark:text-dark-text-muted';
      default: return 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted';
    }
  }

  function priorityClasses(priority: string): string {
    switch (priority) {
      case 'critical': return 'bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-400';
      case 'high': return 'bg-orange-100 dark:bg-orange-900/30 text-orange-700 dark:text-orange-400';
      case 'medium': return 'bg-yellow-100 dark:bg-yellow-900/30 text-yellow-700 dark:text-yellow-400';
      case 'low': return 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted';
      default: return 'bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted';
    }
  }

  // ─── Inline edits ───

  function startEditTitle() {
    if (!task) return;
    editTitle = task.title;
    editingTitle = true;
  }

  async function saveTitle() {
    if (!task || !editTitle.trim()) return;
    saving = true;
    try {
      await updateTask(task.id, { title: editTitle.trim() });
      task.title = editTitle.trim();
      storeNavbar.title = task.title;
      editingTitle = false;
      addToast('Title updated');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update title', 'alert');
    } finally {
      saving = false;
    }
  }

  function cancelEditTitle() {
    editingTitle = false;
  }

  function startEditDescription() {
    if (!task) return;
    editDescription = task.description || '';
    editingDescription = true;
  }

  async function saveDescription() {
    if (!task) return;
    saving = true;
    try {
      await updateTask(task.id, { description: editDescription.trim() });
      task.description = editDescription.trim();
      editingDescription = false;
      addToast('Description updated');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update description', 'alert');
    } finally {
      saving = false;
    }
  }

  function cancelEditDescription() {
    editingDescription = false;
  }

  // ─── Field updates ───

  async function updateField(field: string, value: any) {
    if (!task) return;
    saving = true;
    try {
      await updateTask(task.id, { [field]: value });
      (task as any)[field] = value;
      addToast(`${field.replace(/_/g, ' ')} updated`);
    } catch (e: any) {
      addToast(e?.response?.data?.message || `Failed to update ${field}`, 'alert');
      await loadTask();
    } finally {
      saving = false;
    }
  }

  // ─── Labels ───

  function isLabelAttached(labelId: string): boolean {
    return taskLabels.some(l => l.id === labelId);
  }

  async function toggleLabel(label: Label) {
    try {
      if (isLabelAttached(label.id)) {
        await removeLabelFromTask(params.id, label.id);
        taskLabels = taskLabels.filter(l => l.id !== label.id);
      } else {
        await addLabelToTask(params.id, label.id);
        taskLabels = [...taskLabels, label];
      }
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to update label', 'alert');
    }
  }

  async function handleCreateLabel() {
    const name = newLabelName.trim();
    if (!name) {
      addToast('Label name is required', 'warn');
      return;
    }
    creatingLabel = true;
    try {
      const label = await createLabel({
        name,
        color: newLabelColor,
        organization_id: task?.organization_id || '',
      });
      // Auto-attach the new label to this task
      await addLabelToTask(params.id, label.id);
      taskLabels = [...taskLabels, label];
      allLabels = [...allLabels, label];
      newLabelName = '';
      newLabelColor = '#3b82f6';
      addToast(`Label "${name}" created and added`);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to create label', 'alert');
    } finally {
      creatingLabel = false;
    }
  }

  // ─── Delete ───

  async function handleDelete() {
    if (!task) return;
    try {
      await deleteTask(task.id);
      addToast('Task deleted');
      push('/tasks');
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to delete task', 'alert');
    }
  }

  // ─── Process (delegation) ───

  let processing = $state(false);
  let showRevisionForm = $state(false);
  let revisionFeedback = $state('');
  let requestingRevision = $state(false);

  async function handleRequestRevision() {
    if (!task || !revisionFeedback.trim()) {
      addToast('Please enter feedback for the revision', 'warn');
      return;
    }
    requestingRevision = true;
    try {
      // 1. Add the feedback as a comment
      await createComment(task.id, {
        body: revisionFeedback.trim(),
        author_type: 'user',
        author_id: 'reviewer',
      });
      // 2. Reset task status to open
      await updateTask(task.id, { status: 'open' });
      // 3. Trigger re-processing
      await processTask(task.id);

      task.status = 'open';
      revisionFeedback = '';
      showRevisionForm = false;
      addToast('Revision requested — task sent for re-processing with your feedback');
      // Reload to reflect changes
      await loadTask();
      await loadSubTasks();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to request revision', 'alert');
    } finally {
      requestingRevision = false;
    }
  }

  async function handleProcess() {
    if (!task) return;
    processing = true;
    try {
      await processTask(task.id);
      addToast(`Task "${task.title}" sent for processing`);
      await loadTask();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to process task', 'alert');
    } finally {
      processing = false;
    }
  }

  // ─── Active delegation poll ───
  // Polls /api/v1/active-delegations every 3s while the task is in_progress
  // and intersects the result with the subtask tree, so we know not just
  // "an agent is running" but *which* agent at *which* depth. Used by the
  // "Agent working" pill (sidebar) and the live-highlight in the subtask
  // tree.

  // Collect every task id in the loaded tree (including the root). When a
  // tree node hasn't been loaded yet we fall back to just `task.id`.
  function collectTreeTaskIds(node: TaskWithSubtasks | null): string[] {
    if (!node) return [];
    const out: string[] = [node.id];
    if (node.sub_tasks?.length) {
      for (const child of node.sub_tasks) {
        out.push(...collectTreeTaskIds(child));
      }
    }
    return out;
  }

  // Depth-of-task within the loaded tree, root = 0. Used to surface the
  // *deepest* in-flight delegation as "this agent is working right now",
  // since delegation chains run parent goroutines that block on child
  // goroutines — the leaf is what the user wants to see.
  function depthInTree(node: TaskWithSubtasks | null, taskId: string, depth = 0): number {
    if (!node) return -1;
    if (node.id === taskId) return depth;
    if (!node.sub_tasks?.length) return -1;
    for (const child of node.sub_tasks) {
      const d = depthInTree(child, taskId, depth + 1);
      if (d >= 0) return d;
    }
    return -1;
  }

  async function checkDelegation() {
    if (!task) return;
    try {
      const res = await listActiveDelegations();
      // Collect every task id we know about in this delegation tree.
      const treeIds = new Set<string>(collectTreeTaskIds(taskTree));
      treeIds.add(task.id);

      // Find every active delegation that belongs to this tree.
      const matches = res.delegations.filter((d: ActiveDelegation) =>
        treeIds.has(d.task_id),
      );

      // Pick the deepest one (most recent in the delegation chain) for
      // the "which agent is working" pill. When the tree isn't loaded
      // we fall back to the entry that matches `task.id`.
      let deepest: ActiveDelegation | null = null;
      let deepestDepth = -1;
      for (const m of matches) {
        const depth = depthInTree(taskTree, m.task_id);
        if (depth > deepestDepth) {
          deepestDepth = depth;
          deepest = m;
        }
      }
      // If nothing was depth-resolvable (tree not loaded yet), use the
      // root entry if present.
      if (!deepest && matches.length > 0) {
        deepest = matches.find((m) => m.task_id === task!.id) ?? matches[0];
      }

      const wasActive = delegationActive;
      const nowActive = matches.length > 0;
      delegationActive = nowActive;
      delegationRootActive = !!matches.find((m) => m.task_id === task!.id);
      delegationDuration = deepest?.duration ?? '';
      activeDeepest = deepest;
      activeTaskIds = new Set(matches.map((m) => m.task_id));

      // Refresh on the falling edge: a delegation that *was* running
      // just finished. Without this guard, a task stuck in
      // `in_progress` with no live goroutine would refetch every 3s
      // forever and visibly re-render on every tick.
      if (wasActive && !nowActive && task?.status === 'in_progress') {
        await loadTask();
        await loadSubTasks();
      }
      // Live tree refresh: while a delegation is running, the subtask
      // tree may grow (Content Director just spawned Script Writer).
      // Refresh at a slow cadence (every 4th poll = 12s) so the user
      // sees new nodes appear without spamming the server. Cheap because
      // getTaskWithSubtasks returns the whole tree in one query.
      else if (nowActive) {
        subtaskRefreshTick++;
        if (subtaskRefreshTick % 4 === 0) {
          loadSubTasks();
          // If the user is staring at the Events tab, refresh that too
          // so they see new tool calls / delegations as they happen.
          if (activeTab === 'events') loadEvents();
        }
      }
    } catch {
      delegationActive = false;
      delegationRootActive = false;
      activeDeepest = null;
      activeTaskIds = new Set();
    }
  }

  let subtaskRefreshTick = 0;

  function startDelegationPoll() {
    stopDelegationPoll();
    checkDelegation();
    delegationPollTimer = setInterval(checkDelegation, 3000);
  }

  function stopDelegationPoll() {
    if (delegationPollTimer) {
      clearInterval(delegationPollTimer);
      delegationPollTimer = null;
    }
  }

  // Start/stop polling based on task status. We only react to the status
  // string, not the `task` object reference — `loadTask()` reassigns `task`
  // and would otherwise re-enter this effect, restarting the interval and
  // firing checkDelegation() again on every tick.
  let pollStatus = $derived(task?.status ?? null);
  $effect(() => {
    if (pollStatus === 'in_progress' || pollStatus === 'open') {
      startDelegationPoll();
    } else {
      stopDelegationPoll();
      delegationActive = false;
    }
    return () => stopDelegationPoll();
  });

  async function handleCancelDelegation() {
    if (!task) return;
    cancelling = true;
    try {
      await cancelTaskDelegation(task.id);
      addToast('Cancel signal sent — the agent will stop after the current step');
      // Give it a moment, then refresh
      setTimeout(async () => {
        await checkDelegation();
        await loadTask();
        await loadSubTasks();
      }, 2000);
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'No active delegation to cancel', 'alert');
    } finally {
      cancelling = false;
    }
  }

  // ─── Activity / Chat ───

  let openingChat = $state(false);

  async function handleOpenChat() {
    if (!task) return;
    openingChat = true;
    try {
      const session = await createTaskChat(task.id);
      chatSessionId = session.id;
      activeTab = 'activity';
      await loadChatMessages();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to open chat', 'alert');
    } finally {
      openingChat = false;
    }
  }

  async function loadChatSession() {
    if (!task || chatLoading) return;
    chatLoading = true;
    try {
      const session = await createTaskChat(task.id);
      chatSessionId = session.id;
      await loadChatMessages();
    } catch {
      // Chat not available (no org or no agent assigned)
      chatSessionId = null;
    } finally {
      chatLoading = false;
    }
  }

  async function loadChatMessages() {
    if (!chatSessionId) return;
    try {
      chatMessages = await listChatMessages(chatSessionId);
      scrollChatToBottom();
    } catch (e: any) {
      addToast(e?.response?.data?.message || 'Failed to load messages', 'alert');
    }
  }

  function scrollChatToBottom() {
    setTimeout(() => {
      chatMessagesEnd?.scrollIntoView({ behavior: 'smooth' });
    }, 50);
  }

  function getMessageText(data: any): string {
    if (typeof data?.content === 'string') return data.content;
    if (Array.isArray(data?.content)) {
      return data.content
        .filter((b: any) => b.type === 'text')
        .map((b: any) => b.text)
        .join('');
    }
    return '';
  }

  function handleChatSend() {
    if (!chatSessionId || !chatInput.trim() || chatSending) return;
    const content = chatInput.trim();
    chatInput = '';
    chatSending = true;
    chatStreamContent = '';
    chatToolEvents = [];

    // Optimistic user message
    chatMessages = [
      ...chatMessages,
      {
        id: `pending-${Date.now()}`,
        session_id: chatSessionId,
        role: 'user',
        data: { content },
        created_at: new Date().toISOString(),
      },
    ];
    scrollChatToBottom();

    chatAbortController = sendChatMessage(
      chatSessionId,
      content,
      (event) => {
        if (event.type === 'content') {
          chatStreamContent += event.content;
          scrollChatToBottom();
        } else if (event.type === 'tool_call') {
          chatToolEvents = [...chatToolEvents, { type: 'call', name: event.tool_name, id: event.tool_id }];
          scrollChatToBottom();
        } else if (event.type === 'tool_result') {
          chatToolEvents = [...chatToolEvents, { type: 'result', name: event.tool_name, id: event.tool_id, result: event.result }];
          scrollChatToBottom();
        }
      },
      (error) => {
        addToast(error, 'alert');
        chatSending = false;
        chatAbortController = null;
      },
      async () => {
        chatSending = false;
        chatAbortController = null;
        chatStreamContent = '';
        chatToolEvents = [];
        await loadChatMessages();
        // Refresh task to pick up status/result changes
        await loadTask();
        await loadSubTasks();
      },
    );
  }

  function stopChatGeneration() {
    chatAbortController?.abort();
    chatAbortController = null;
    chatSending = false;
    chatStreamContent = '';
    chatToolEvents = [];
  }

  function handleChatKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleChatSend();
    }
  }

  // Auto-load chat session when Activity tab is first opened
  $effect(() => {
    if (activeTab === 'activity' && !chatSessionId && !chatLoading && task) {
      loadChatSession();
    }
  });
  // ─── Tree toggle ───

  function toggleNode(nodeId: string) {
    const next = new Set(expandedNodes);
    if (next.has(nodeId)) {
      next.delete(nodeId);
    } else {
      next.add(nodeId);
    }
    expandedNodes = next;
  }
</script>

<svelte:head>
  <title>AT | {task?.title || 'Task Detail'}</title>
</svelte:head>

{#snippet delegationNode(node: TaskWithSubtasks, depth: number)}
  {@const isLive = activeTaskIds.has(node.id)}
  <div class="subtask-row {isLive ? 'subtask-row-live' : ''}" style="padding-left: {depth * 20}px">
    <div class="subtask-inner flex items-center gap-2 px-3 py-2 transition-colors">
      <!-- Expand/collapse toggle -->
      {#if node.sub_tasks?.length}
        <button
          onclick={() => toggleNode(node.id)}
          class="p-0.5 text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary transition-colors shrink-0"
        >
          {#if expandedNodes.has(node.id)}
            <ChevronDown size={12} />
          {:else}
            <ChevronRight size={12} />
          {/if}
        </button>
      {:else}
        <span class="w-4 shrink-0"></span>
      {/if}

      <!-- Live pulse: this subtask currently has a running delegation goroutine. -->
      {#if isLive}
        <span class="relative flex w-2 h-2 shrink-0" title="Agent is working on this subtask right now">
          <span class="absolute inline-flex w-full h-full rounded-full bg-green-400 opacity-75 animate-ping"></span>
          <span class="relative inline-flex w-2 h-2 rounded-full bg-green-500"></span>
        </span>
      {/if}

      <!-- Status badge -->
      <span class="inline-block px-2 py-0.5 text-[10px] font-medium capitalize shrink-0 {statusClasses(node.status)}">
        {TASK_STATUS_LABELS[node.status] || node.status}
      </span>

      <!-- Identifier -->
      {#if node.identifier}
        <span class="text-[10px] font-mono text-gray-400 dark:text-dark-text-muted shrink-0">{node.identifier}</span>
      {/if}

      <!-- Title (clickable link) -->
      <a
        href="#/tasks/{node.id}"
        class="text-sm text-gray-900 dark:text-dark-text hover:text-blue-600 dark:hover:text-blue-400 transition-colors truncate flex-1 {isLive ? 'font-medium' : ''}"
      >
        {node.title}
      </a>

      <!-- Assigned agent -->
      {#if node.assigned_agent_id}
        <span class="flex items-center gap-1 text-[10px] {isLive ? 'text-green-700 dark:text-green-400 font-medium' : 'text-gray-400 dark:text-dark-text-muted'} shrink-0" title="{isLive ? 'Currently working: ' : 'Assigned to '}{agentDisplayName(node.assigned_agent_id)}">
          <User size={10} />
          <span class="max-w-[100px] truncate">{agentDisplayName(node.assigned_agent_id)}</span>
        </span>
      {/if}

      <!-- Child count indicator -->
      {#if node.sub_tasks?.length}
        <span class="text-[10px] text-gray-400 dark:text-dark-text-muted shrink-0">
          {node.sub_tasks.length} sub
        </span>
      {/if}
    </div>

    <!-- Recursive children -->
    {#if node.sub_tasks?.length && expandedNodes.has(node.id)}
      {#each node.sub_tasks as child}
        {@render delegationNode(child, depth + 1)}
      {/each}
    {/if}
  </div>
{/snippet}

{#if loading}
  <div class="flex items-center justify-center h-full">
    <div class="text-sm text-gray-400 dark:text-dark-text-muted">Loading task...</div>
  </div>
{:else if task}
  <div class="h-full overflow-y-auto">
    <div class="max-w-6xl mx-auto p-6">
      <!-- Back navigation + Refresh -->
      <div class="flex items-center justify-between mb-4">
        <button
          onclick={() => push('/tasks')}
          class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
        >
          <ArrowLeft size={14} />
          Back to Tasks
        </button>
        <button
          onclick={() => { loadTask(); loadLabels(); loadSubTasks(); }}
          disabled={loading}
          class="flex items-center gap-1.5 px-2 py-1 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated disabled:opacity-50 transition-colors"
          title="Refresh task"
        >
          <RefreshCw size={13} class={loading ? 'animate-spin' : ''} />
          Refresh
        </button>
      </div>

      <div class="flex gap-6">
        <!-- Main content -->
        <div class="flex-1 min-w-0 space-y-6">
          <!-- Title -->
          <div class="group">
            {#if editingTitle}
              <div class="flex items-center gap-2">
                <input
                  type="text"
                  bind:value={editTitle}
                  onkeydown={(e) => { if (e.key === 'Enter') saveTitle(); if (e.key === 'Escape') cancelEditTitle(); }}
                  class="flex-1 text-xl font-semibold border border-gray-300 dark:border-dark-border-subtle px-3 py-1.5 focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text transition-colors"
                />
                <button onclick={saveTitle} disabled={saving}
                  class="p-1.5 bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors" title="Save">
                  <Check size={14} />
                </button>
                <button onclick={cancelEditTitle}
                  class="p-1.5 border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-500 transition-colors" title="Cancel">
                  <X size={14} />
                </button>
              </div>
            {:else}
              <div class="flex items-start gap-2">
                <h1 class="text-xl font-semibold text-gray-900 dark:text-dark-text break-words flex-1">
                  {#if task.identifier}
                    <span class="text-sm font-mono text-gray-400 dark:text-dark-text-muted mr-2">{task.identifier}</span>
                  {/if}
                  {task.title}
                </h1>
                <button onclick={startEditTitle}
                  class="p-1.5 opacity-0 group-hover:opacity-100 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-all shrink-0" title="Edit title">
                  <Pencil size={14} />
                </button>
              </div>
            {/if}
          </div>

          <!-- Description -->
          <div class="group">
            <div class="flex items-center justify-between mb-1">
              <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Description</span>
              {#if !editingDescription}
                <button onclick={startEditDescription}
                  class="p-1 opacity-0 group-hover:opacity-100 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text transition-all" title="Edit description">
                  <Pencil size={12} />
                </button>
              {/if}
            </div>

            {#if editingDescription}
              <div class="space-y-2">
                <textarea
                  bind:value={editDescription}
                  rows="5"
                  class="w-full border border-gray-300 dark:border-dark-border-subtle px-3 py-2 text-sm focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 dark:bg-dark-elevated dark:text-dark-text transition-colors resize-y"
                  placeholder="Add a description..."
                ></textarea>
                <div class="flex gap-2">
                  <button onclick={saveDescription} disabled={saving}
                    class="flex items-center gap-1.5 px-3 py-1.5 text-xs bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:hover:bg-accent-hover transition-colors">
                    <Save size={12} /> Save
                  </button>
                  <button onclick={cancelEditDescription}
                    class="px-3 py-1.5 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-700 dark:text-dark-text-secondary transition-colors">
                    Cancel
                  </button>
                </div>
              </div>
            {:else}
              {#if task.description}
                <Markdown
                  source={task.description}
                  class="text-sm text-gray-700 dark:text-dark-text-secondary leading-relaxed min-h-[2rem]"
                  enhance
                />
              {:else}
                <div class="text-sm min-h-[2rem]">
                  <span class="text-gray-400 dark:text-dark-text-muted italic">No description</span>
                </div>
              {/if}
            {/if}
          </div>

          <!-- Result -->
          {#if task.result}
            <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
              <div class="px-3 py-2 border-b border-gray-100 dark:border-dark-border">
                <span class="text-xs font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Result</span>
              </div>
              <div class="px-3 py-3">
                <Markdown
                  source={task.result}
                  class="text-sm text-gray-700 dark:text-dark-text-secondary leading-relaxed break-words"
                  enhance
                />
              </div>
            </div>
          {/if}

          <!-- Tabs -->
          <div class="border-b border-gray-200 dark:border-dark-border">
            <div class="flex gap-0">
              <button
                onclick={() => (activeTab = 'activity')}
                class="flex items-center gap-1.5 px-4 py-2 text-xs font-medium transition-colors border-b-2 {activeTab === 'activity' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
              >
                <Activity size={13} />
                Activity
                {#if chatSending}
                  <Loader2 size={10} class="animate-spin text-green-500" />
                {/if}
              </button>
              <button
                onclick={() => (activeTab = 'comments')}
                class="flex items-center gap-1.5 px-4 py-2 text-xs font-medium transition-colors border-b-2 {activeTab === 'comments' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
              >
                <MessageSquare size={13} />
                Comments
              </button>
              <button
                onclick={() => (activeTab = 'subtasks')}
                class="flex items-center gap-1.5 px-4 py-2 text-xs font-medium transition-colors border-b-2 {activeTab === 'subtasks' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
              >
                <ListTree size={13} />
                Sub-tasks
                {#if taskTree?.sub_tasks?.length}
                  <span class="ml-1 px-1.5 py-0 text-[10px] bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted">{taskTree.sub_tasks.length}</span>
                {/if}
                {#if delegationActive}
                  <span class="relative flex w-1.5 h-1.5 ml-0.5" title="Live delegation in this tree">
                    <span class="absolute inline-flex w-full h-full rounded-full bg-green-400 opacity-75 animate-ping"></span>
                    <span class="relative inline-flex w-1.5 h-1.5 rounded-full bg-green-500"></span>
                  </span>
                {/if}
              </button>
              <button
                onclick={() => (activeTab = 'events')}
                class="flex items-center gap-1.5 px-4 py-2 text-xs font-medium transition-colors border-b-2 {activeTab === 'events' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
                title="Live audit timeline for this task and its delegation tree"
              >
                <Clock size={13} />
                Events
                {#if delegationActive}
                  <span class="relative flex w-1.5 h-1.5 ml-0.5">
                    <span class="absolute inline-flex w-full h-full rounded-full bg-green-400 opacity-75 animate-ping"></span>
                    <span class="relative inline-flex w-1.5 h-1.5 rounded-full bg-green-500"></span>
                  </span>
                {/if}
              </button>
              <button
                onclick={() => (activeTab = 'labels')}
                class="flex items-center gap-1.5 px-4 py-2 text-xs font-medium transition-colors border-b-2 {activeTab === 'labels' ? 'border-gray-900 dark:border-accent text-gray-900 dark:text-dark-text' : 'border-transparent text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary'}"
              >
                <Tag size={13} />
                Labels
                {#if taskLabels.length > 0}
                  <span class="ml-1 px-1.5 py-0 text-[10px] bg-gray-100 dark:bg-dark-elevated text-gray-600 dark:text-dark-text-muted">{taskLabels.length}</span>
                {/if}
              </button>
            </div>
          </div>

          <!-- Tab content -->
          <div class="min-h-[200px]">
            {#if activeTab === 'activity'}
              <!-- ─── Activity / Chat panel ─── -->
              <div class="flex flex-col h-[550px] border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface overflow-hidden">
                <!-- Chat messages area -->
                <div class="flex-1 overflow-y-auto min-h-0">
                  {#if chatLoading}
                    <div class="flex items-center justify-center h-full">
                      <div class="flex items-center gap-2 text-xs text-gray-400 dark:text-dark-text-muted">
                        <Loader2 size={14} class="animate-spin" />
                        Loading chat session...
                      </div>
                    </div>
                  {:else if !chatSessionId}
                    <div class="flex flex-col items-center justify-center h-full text-center px-6">
                      <Activity size={24} class="text-gray-300 dark:text-dark-text-faint mb-3" />
                      <p class="text-sm text-gray-500 dark:text-dark-text-muted mb-1">No chat session available</p>
                      <p class="text-[11px] text-gray-400 dark:text-dark-text-muted mb-4">
                        This task needs an organization with an assigned agent to enable chat.
                      </p>
                      {#if task?.organization_id}
                        <button
                          onclick={handleOpenChat}
                          disabled={openingChat}
                          class="flex items-center gap-1.5 px-3 py-1.5 text-xs font-medium bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
                        >
                          <MessageSquare size={12} />
                          {openingChat ? 'Starting...' : 'Start Chat Session'}
                        </button>
                      {/if}
                    </div>
                  {:else}
                    <div class="px-4 py-3 space-y-1">
                      {#if chatMessages.length === 0 && !chatSending}
                        <div class="flex flex-col items-center justify-center py-12 text-center">
                          <MessageSquare size={20} class="text-gray-300 dark:text-dark-text-faint mb-2" />
                          <p class="text-xs text-gray-400 dark:text-dark-text-muted">
                            Send a message to start chatting with the agent.
                          </p>
                        </div>
                      {/if}

                      {#each chatMessages as msg (msg.id)}
                        {#if msg.role === 'user'}
                          <div class="py-1.5">
                            <div class="flex items-baseline gap-2">
                              <span class="text-[11px] font-bold text-blue-600 dark:text-blue-400 select-none shrink-0">you</span>
                              <span class="text-[10px] text-gray-300 dark:text-dark-text-muted select-none">{formatDateTime(msg.created_at)}</span>
                            </div>
                            <div class="mt-0.5 text-[13px] text-gray-800 dark:text-dark-text whitespace-pre-wrap">{getMessageText(msg.data)}</div>
                          </div>
                        {:else if msg.role === 'assistant'}
                          <div class="py-1.5">
                            <div class="flex items-baseline gap-2">
                              <span class="text-[11px] font-bold text-green-600 dark:text-green-400 select-none shrink-0">assistant</span>
                              <span class="text-[10px] text-gray-300 dark:text-dark-text-muted select-none">{formatDateTime(msg.created_at)}</span>
                              {#if msg.data.tool_calls}
                                <span class="text-[10px] text-yellow-600 dark:text-yellow-400">
                                  [{Array.isArray(msg.data.tool_calls) ? msg.data.tool_calls.map((tc: any) => tc.Name || tc.name || tc.function?.name).join(', ') : 'tools'}]
                                </span>
                              {/if}
                            </div>
                            <Markdown
                              source={getMessageText(msg.data)}
                              class="mt-0.5 max-w-none text-[13px] leading-relaxed"
                            />
                          </div>
                        {:else if msg.role === 'tool'}
                          {@const toolText = getMessageText(msg.data)}
                          {@const toolId = `tool-${msg.id}`}
                          <div class="py-0.5 pl-4 border-l-2 border-gray-200 dark:border-dark-border">
                            <button
                              class="text-[10px] text-gray-400 dark:text-dark-text-muted hover:text-gray-600 dark:hover:text-dark-text-secondary"
                              onclick={() => { chatExpandedTools[toolId] = !chatExpandedTools[toolId]; }}
                            >
                              tool {#if msg.data.tool_call_id}<span class="text-gray-500">{msg.data.tool_call_id.slice(0, 12)}</span>{/if}
                              <span class="ml-1">{chatExpandedTools[toolId] ? '▼' : '▶'} {toolText.length > 150 ? `${toolText.length} chars` : ''}</span>
                            </button>
                            {#if chatExpandedTools[toolId]}
                              <pre class="text-[11px] text-gray-500 dark:text-dark-text-secondary whitespace-pre-wrap break-all mt-0.5 max-h-96 overflow-y-auto bg-gray-50 dark:bg-dark-base p-2 border border-gray-200 dark:border-dark-border">{toolText}</pre>
                            {:else}
                              <pre class="text-[11px] text-gray-500 dark:text-dark-text-secondary whitespace-pre-wrap break-all mt-0.5 max-h-8 overflow-hidden">{toolText.slice(0, 150)}{toolText.length > 150 ? '...' : ''}</pre>
                            {/if}
                          </div>
                        {/if}
                      {/each}

                      <!-- Streaming tool events -->
                      {#if chatToolEvents.length > 0}
                        <div class="py-0.5 pl-4 border-l-2 border-yellow-300 dark:border-yellow-600">
                          {#each chatToolEvents as evt}
                            {#if evt.type === 'call'}
                              <div class="flex items-center gap-1 text-[11px] text-yellow-700 dark:text-yellow-400">
                                <Loader2 size={10} class="animate-spin" />
                                <span>{evt.name}</span>
                              </div>
                            {:else}
                              {@const evtResult = evt.result || ''}
                              {@const evtId = `stream-${evt.id || evt.name}`}
                              <button
                                class="text-[10px] text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary text-left"
                                onclick={() => { chatExpandedTools[evtId] = !chatExpandedTools[evtId]; }}
                              >
                                <span class="text-green-600 dark:text-green-400">{evt.name}</span>
                                {#if chatExpandedTools[evtId]}
                                  <span class="ml-1">▼</span>
                                {:else}
                                  <span class="ml-1">{evtResult.length > 100 ? '▶' : '→'}</span>
                                  <span class="font-mono">{evtResult.slice(0, 150)}{evtResult.length > 150 ? '...' : ''}</span>
                                {/if}
                              </button>
                              {#if chatExpandedTools[evtId]}
                                <pre class="mt-0.5 text-[11px] font-mono whitespace-pre-wrap break-all max-h-96 overflow-y-auto bg-gray-50 dark:bg-dark-base p-2 border border-gray-200 dark:border-dark-border">{evtResult}</pre>
                              {/if}
                            {/if}
                          {/each}
                        </div>
                      {/if}

                      <!-- Streaming assistant content -->
                      {#if chatStreamContent}
                        <div class="py-1.5">
                          <div class="flex items-baseline gap-2">
                            <span class="text-[11px] font-bold text-green-600 dark:text-green-400 select-none">assistant</span>
                            {#if chatSending}
                              <Loader2 size={10} class="animate-spin text-gray-400" />
                            {/if}
                          </div>
                          <Markdown
                            source={chatStreamContent}
                            class="mt-0.5 max-w-none text-[13px] leading-relaxed"
                          />
                        </div>
                      {/if}

                      <div bind:this={chatMessagesEnd}></div>
                    </div>
                  {/if}
                </div>

                <!-- Input bar -->
                {#if chatSessionId}
                  <div class="border-t border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-elevated px-3 py-2 shrink-0">
                    <div class="flex items-center gap-2">
                      <textarea
                        bind:this={chatInputEl}
                        bind:value={chatInput}
                        onkeydown={handleChatKeydown}
                        placeholder="Message the agent... (Enter to send)"
                        rows={1}
                        disabled={chatSending}
                        class="flex-1 resize-none bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border px-3 py-1.5 text-[13px] text-gray-800 dark:text-dark-text placeholder:text-gray-400 dark:placeholder:text-dark-text-muted focus:outline-none focus:ring-2 focus:ring-gray-900/10 dark:focus:ring-accent/20 disabled:opacity-50"
                      ></textarea>
                      {#if chatSending}
                        <button
                          onclick={stopChatGeneration}
                          class="p-1.5 text-red-500 hover:text-red-600 hover:bg-red-50 dark:hover:bg-red-900/20 transition-colors shrink-0"
                          title="Stop generation"
                        >
                          <Square size={14} />
                        </button>
                      {:else}
                        <button
                          onclick={handleChatSend}
                          disabled={!chatInput.trim()}
                          class="p-1.5 text-gray-500 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated disabled:opacity-20 transition-colors shrink-0"
                          title="Send"
                        >
                          <Send size={14} />
                        </button>
                      {/if}
                    </div>
                  </div>
                {/if}
              </div>
            {:else if activeTab === 'comments'}
              <CommentThread taskId={params.id} />
            {:else if activeTab === 'subtasks'}
              {#if subTasksLoading}
                <div class="text-sm text-gray-400 dark:text-dark-text-muted py-8 text-center">Loading delegation tree...</div>
              {:else if !taskTree?.sub_tasks?.length}
                <div class="text-sm text-gray-400 dark:text-dark-text-muted py-8 text-center flex flex-col items-center gap-2">
                  <ListTree size={20} class="text-gray-300 dark:text-dark-text-faint" />
                  <span>No delegation chain</span>
                  <span class="text-[10px]">Sub-tasks created by delegation will appear here as a tree</span>
                </div>
              {:else}
                <div class="subtask-list">
                  {#each taskTree.sub_tasks as node}
                    {@render delegationNode(node, 0)}
                  {/each}
                </div>
              {/if}
            {:else if activeTab === 'events'}
              <!-- ─── Live audit timeline for the whole delegation tree ─── -->
              <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
                <div class="px-3 py-2 border-b border-gray-100 dark:border-dark-border flex items-center justify-between">
                  <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Audit Timeline</span>
                  <button
                    onclick={loadEvents}
                    class="flex items-center gap-1 text-[10px] text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors"
                    title="Refresh"
                  >
                    <RefreshCw size={11} class={eventsLoading ? 'animate-spin' : ''} />
                    Refresh
                  </button>
                </div>
                {#if eventsLoading && events.length === 0}
                  <div class="text-sm text-gray-400 dark:text-dark-text-muted py-8 text-center">Loading events...</div>
                {:else if events.length === 0}
                  <div class="text-sm text-gray-400 dark:text-dark-text-muted py-8 text-center flex flex-col items-center gap-2">
                    <Clock size={18} class="text-gray-300 dark:text-dark-text-faint" />
                    <span>No events yet</span>
                    <span class="text-[10px]">Tool calls, delegations, and status changes will appear here as the agent works</span>
                  </div>
                {:else}
                  <div class="max-h-[550px] overflow-y-auto">
                    {#each events as e (e.id)}
                      {@const isLive = activeTaskIds.has(e.resource_id)}
                      <div class="px-3 py-2 border-b border-gray-100 dark:border-dark-border-subtle flex items-start gap-2 text-xs {isLive ? 'bg-green-50/50 dark:bg-green-900/10' : ''}">
                        <span class="text-[10px] text-gray-400 dark:text-dark-text-muted font-mono shrink-0 w-[140px]" title={e.created_at}>
                          {formatDateTime(e.created_at)}
                        </span>
                        <span class="font-medium text-gray-700 dark:text-dark-text-secondary shrink-0 max-w-[140px] truncate">
                          {eventActor(e)}
                        </span>
                        <span class="text-gray-500 dark:text-dark-text-muted shrink-0">{eventLabel(e)}</span>
                        {#if e.resource_type === 'task' && e.resource_id !== task.id}
                          <a
                            href="#/tasks/{e.resource_id}"
                            class="text-blue-600 dark:text-blue-400 hover:underline truncate flex-1 min-w-0"
                            title={eventTaskTitle(e) || e.resource_id}
                          >
                            {eventTaskTitle(e) || e.resource_id.slice(0, 12)}
                          </a>
                        {/if}
                        {#if isLive}
                          <span class="ml-auto relative flex w-1.5 h-1.5 shrink-0" title="In flight">
                            <span class="absolute inline-flex w-full h-full rounded-full bg-green-400 opacity-75 animate-ping"></span>
                            <span class="relative inline-flex w-1.5 h-1.5 rounded-full bg-green-500"></span>
                          </span>
                        {/if}
                      </div>
                    {/each}
                  </div>
                {/if}
              </div>
              <p class="text-[10px] text-gray-400 dark:text-dark-text-muted mt-2 italic">
                Showing the most recent {events.length} audit entries across this task and its sub-tasks.
                The list refreshes when you switch back to this tab; sub-tasks themselves auto-refresh while a delegation is running.
              </p>
            {:else if activeTab === 'labels'}
              {#if labelsLoading}
                <div class="text-sm text-gray-400 dark:text-dark-text-muted py-8 text-center">Loading labels...</div>
              {:else}
                <!-- Attached labels -->
                {#if taskLabels.length > 0}
                  <div class="flex flex-wrap gap-2 mb-4">
                    {#each taskLabels as label}
                      <span class="inline-flex items-center gap-1.5 px-2.5 py-1 text-xs font-medium border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
                        {#if label.color}
                          <span class="w-2.5 h-2.5 rounded-full shrink-0" style="background-color: {label.color}"></span>
                        {/if}
                        {label.name}
                        <button
                          onclick={() => toggleLabel(label)}
                          class="ml-1 p-0.5 hover:bg-gray-100 dark:hover:bg-dark-elevated text-gray-400 hover:text-red-500 transition-colors"
                          title="Remove label"
                        >
                          <X size={10} />
                        </button>
                      </span>
                    {/each}
                  </div>
                {/if}

                <!-- Add label -->
                <button
                  onclick={() => (showLabelPicker = !showLabelPicker)}
                  class="flex items-center gap-1.5 text-xs text-gray-500 dark:text-dark-text-muted hover:text-gray-700 dark:hover:text-dark-text-secondary transition-colors mb-3"
                >
                  <Tag size={12} />
                  {showLabelPicker ? 'Hide label picker' : 'Add label'}
                </button>

                {#if showLabelPicker}
                  <!-- Inline create label -->
                  <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3 mb-2">
                    <div class="flex items-center gap-2 mb-2">
                      <input
                        type="text"
                        bind:value={newLabelName}
                        placeholder="New label name..."
                        class="flex-1 border border-gray-200 dark:border-dark-border px-2 py-1.5 text-xs bg-transparent dark:text-dark-text focus:outline-none focus:border-gray-400"
                        onkeydown={(e) => { if (e.key === 'Enter') handleCreateLabel(); }}
                      />
                      <button
                        onclick={handleCreateLabel}
                        disabled={creatingLabel || !newLabelName.trim()}
                        class="px-2.5 py-1.5 text-xs bg-gray-900 dark:bg-accent text-white hover:bg-gray-800 dark:hover:bg-accent-hover transition-colors disabled:opacity-50"
                      >
                        {creatingLabel ? '...' : 'Create'}
                      </button>
                    </div>
                    <div class="flex items-center gap-1.5">
                      <span class="text-[10px] text-gray-400 dark:text-dark-text-muted mr-1">Color:</span>
                      {#each LABEL_COLOR_PRESETS as color}
                        <button
                          onclick={() => (newLabelColor = color)}
                          class={[
                            'w-5 h-5 rounded-full border-2 transition-all',
                            newLabelColor === color ? 'border-gray-900 dark:border-white scale-110' : 'border-transparent hover:border-gray-300 dark:hover:border-dark-border-subtle',
                          ]}
                          style="background-color: {color}"
                          title={color}
                        ></button>
                      {/each}
                    </div>
                  </div>

                  <!-- Existing labels list -->
                  {#if allLabels.length > 0}
                    <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface max-h-48 overflow-y-auto">
                      {#each allLabels as label}
                        <button
                          onclick={() => toggleLabel(label)}
                          class="flex items-center gap-2 w-full px-3 py-2 text-sm text-left hover:bg-gray-50 dark:hover:bg-dark-elevated/50 transition-colors {isLabelAttached(label.id) ? 'bg-gray-50 dark:bg-dark-elevated/30' : ''}"
                        >
                          {#if label.color}
                            <span class="w-3 h-3 rounded-full shrink-0" style="background-color: {label.color}"></span>
                          {/if}
                          <span class="flex-1 text-gray-700 dark:text-dark-text-secondary">{label.name}</span>
                          {#if isLabelAttached(label.id)}
                            <Check size={12} class="text-green-600 dark:text-green-400" />
                          {/if}
                        </button>
                      {/each}
                    </div>
                  {/if}
                {/if}
              {/if}
            {/if}
          </div>
        </div>

        <!-- Side panel -->
        <div class="w-72 shrink-0 space-y-4">
          <!-- Status -->
          <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
            <div class="px-3 py-2 border-b border-gray-100 dark:border-dark-border">
              <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Status</span>
            </div>
            <div class="px-3 py-2">
              <select
                value={task.status}
                onchange={(e) => updateField('status', (e.target as HTMLSelectElement).value)}
                class="w-full border border-gray-200 dark:border-dark-border-subtle px-2 py-1.5 text-sm focus:outline-none dark:bg-dark-elevated dark:text-dark-text transition-colors"
              >
                {#each TASK_STATUSES as status}
                  <option value={status}>{TASK_STATUS_LABELS[status]}</option>
                {/each}
                {#if !TASK_STATUSES.includes(task.status as any)}
                  <option value={task.status}>{TASK_STATUS_LABELS[task.status] || task.status}</option>
                {/if}
              </select>
            </div>
          </div>

          <!-- Priority -->
          <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
            <div class="px-3 py-2 border-b border-gray-100 dark:border-dark-border">
              <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Priority</span>
            </div>
            <div class="px-3 py-2">
              <select
                value={task.priority_level || ''}
                onchange={(e) => updateField('priority_level', (e.target as HTMLSelectElement).value)}
                class="w-full border border-gray-200 dark:border-dark-border-subtle px-2 py-1.5 text-sm focus:outline-none dark:bg-dark-elevated dark:text-dark-text transition-colors"
              >
                <option value="">None</option>
                {#each TASK_PRIORITIES as prio}
                  <option value={prio}>{TASK_PRIORITY_LABELS[prio]}</option>
                {/each}
              </select>
            </div>
          </div>

          <!-- Properties -->
          <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
            <div class="px-3 py-2 border-b border-gray-100 dark:border-dark-border">
              <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Properties</span>
            </div>
            <div class="divide-y divide-gray-100 dark:divide-dark-border text-sm">
              <!-- Organization -->
              <div class="px-3 py-2 flex items-center gap-2">
                <Building2 size={12} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Organization</span>
                <select
                  value={task.organization_id || ''}
                  onchange={(e) => updateField('organization_id', (e.target as HTMLSelectElement).value)}
                  class="flex-1 min-w-0 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs focus:outline-none dark:bg-dark-elevated dark:text-dark-text transition-colors"
                >
                  <option value="">None</option>
                  {#each organizations as org}
                    <option value={org.id}>{org.name}</option>
                  {/each}
                </select>
              </div>

              <!-- Assigned Agent -->
              <div class="px-3 py-2 flex items-center gap-2">
                <User size={12} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Agent</span>
                <select
                  value={task.assigned_agent_id || ''}
                  onchange={(e) => updateField('assigned_agent_id', (e.target as HTMLSelectElement).value)}
                  class="flex-1 min-w-0 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs focus:outline-none dark:bg-dark-elevated dark:text-dark-text transition-colors"
                >
                  <option value="">Unassigned</option>
                  {#each agents as agent}
                    <option value={agent.id}>{agent.name}</option>
                  {/each}
                </select>
              </div>

              <!-- Project -->
              <div class="px-3 py-2 flex items-center gap-2">
                <FolderOpen size={12} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Project</span>
                <span class="text-xs font-mono text-gray-700 dark:text-dark-text-secondary truncate"
                  title={task.project_id || ''}>
                  {task.project_id || '-'}
                </span>
              </div>

              <!-- Goal -->
              <div class="px-3 py-2 flex items-center gap-2">
                <Layers size={12} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Goal</span>
                <span class="text-xs font-mono text-gray-700 dark:text-dark-text-secondary truncate"
                  title={task.goal_id || ''}>
                  {task.goal_id || '-'}
                </span>
              </div>

              <!-- Parent Task -->
              {#if task.parent_id}
                <div class="px-3 py-2 flex items-center gap-2">
                  <ListTree size={12} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                  <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Parent</span>
                  <a href="#/tasks/{task.parent_id}" class="text-xs font-mono text-blue-600 dark:text-blue-400 hover:underline truncate">
                    {task.parent_id}
                  </a>
                </div>
              {/if}

              <!-- Billing Code -->
              {#if task.billing_code}
                <div class="px-3 py-2 flex items-center gap-2">
                  <CreditCard size={12} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                  <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Billing</span>
                  <span class="text-xs font-mono text-gray-700 dark:text-dark-text-secondary truncate">
                    {task.billing_code}
                  </span>
                </div>
              {/if}

              <!-- Rolled-up cost across this task + all descendants. Loaded
                   lazily via a button (the BFS over sub-tasks isn't free). -->
              <div class="px-3 py-2 flex items-center gap-2">
                <CreditCard size={12} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Cost</span>
                <div class="flex-1 flex items-center gap-2 min-w-0">
                  {#if costRollup}
                    <span class="text-xs font-mono text-gray-900 dark:text-dark-text font-medium" title={`${costRollup.event_count} events across ${costRollup.task_count} task(s)`}>
                      {formatCostCents(costRollup.cost_cents)}
                    </span>
                    <span class="text-[10px] text-gray-400 dark:text-dark-text-muted">
                      · {formatTokensShort(costRollup.total_tokens)} tok · {costRollup.event_count} ev
                    </span>
                    <button
                      onclick={loadCost}
                      disabled={costLoading}
                      class="ml-auto p-0.5 text-gray-400 hover:text-gray-700 dark:text-dark-text-muted dark:hover:text-dark-text disabled:opacity-50 transition-colors"
                      title="Refresh cost"
                    >
                      <RefreshCw size={10} class={costLoading ? 'animate-spin' : ''} />
                    </button>
                    <a
                      href={costEventsUrl}
                      class="p-0.5 text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-300 transition-colors"
                      title="View cost events for this task tree"
                    >
                      <Receipt size={10} />
                    </a>
                  {:else}
                    <button
                      onclick={loadCost}
                      disabled={costLoading}
                      class="text-xs text-blue-600 dark:text-blue-400 hover:underline disabled:opacity-50"
                    >
                      {costLoading ? 'Loading...' : 'Show cost'}
                    </button>
                  {/if}
                </div>
              </div>
              <!-- end Cost -->
              

              <!-- Request Depth -->
              {#if task.request_depth}
                <div class="px-3 py-2 flex items-center gap-2">
                  <Hash size={12} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                  <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Depth</span>
                  <span class="text-xs text-gray-700 dark:text-dark-text-secondary">
                    {task.request_depth}
                  </span>
                </div>
              {/if}

              <!-- Max Iterations (per-task override) -->
              <div class="px-3 py-2 flex items-center gap-2"
                title="Per-task override of the agent's max_iterations. 0 = use agent default. Counter resets to 0 every time this task is processed.">
                <Hash size={12} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Max iter</span>
                <input
                  type="number"
                  min="0"
                  value={task.max_iterations ?? 0}
                  onchange={(e) => {
                    const n = parseInt((e.target as HTMLInputElement).value, 10);
                    updateField('max_iterations', Number.isFinite(n) && n >= 0 ? n : 0);
                  }}
                  placeholder="0 = agent default"
                  class="flex-1 min-w-0 border border-gray-200 dark:border-dark-border-subtle px-1.5 py-0.5 text-xs focus:outline-none dark:bg-dark-elevated dark:text-dark-text transition-colors"
                />
              </div>

              <!-- Checked Out By -->
              {#if task.checked_out_by}
                <div class="px-3 py-2 flex items-center gap-2">
                  <AlertTriangle size={12} class="text-yellow-500 shrink-0" />
                  <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Checked out</span>
                  <span class="text-xs text-gray-700 dark:text-dark-text-secondary truncate"
                    title={task.checked_out_by}>
                    {agentDisplayName(task.checked_out_by)}
                  </span>
                </div>
              {/if}
            </div>
          </div>

          <!-- Dates -->
          <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
            <div class="px-3 py-2 border-b border-gray-100 dark:border-dark-border">
              <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Dates</span>
            </div>
            <div class="divide-y divide-gray-100 dark:divide-dark-border text-sm">
              <div class="px-3 py-2 flex items-center gap-2">
                <Calendar size={12} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Created</span>
                <span class="text-xs text-gray-700 dark:text-dark-text-secondary">{formatDateTime(task.created_at)}</span>
              </div>
              <div class="px-3 py-2 flex items-center gap-2">
                <Clock size={12} class="text-gray-400 dark:text-dark-text-muted shrink-0" />
                <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Updated</span>
                <span class="text-xs text-gray-700 dark:text-dark-text-secondary">{formatDateTime(task.updated_at)}</span>
              </div>
              {#if task.started_at}
                <div class="px-3 py-2 flex items-center gap-2">
                  <Clock size={12} class="text-green-500 shrink-0" />
                  <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Started</span>
                  <span class="text-xs text-gray-700 dark:text-dark-text-secondary">{formatDateTime(task.started_at)}</span>
                </div>
              {/if}
              {#if task.completed_at}
                <div class="px-3 py-2 flex items-center gap-2">
                  <Check size={12} class="text-green-500 shrink-0" />
                  <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Completed</span>
                  <span class="text-xs text-gray-700 dark:text-dark-text-secondary">{formatDateTime(task.completed_at)}</span>
                </div>
              {/if}
              {#if task.cancelled_at}
                <div class="px-3 py-2 flex items-center gap-2">
                  <X size={12} class="text-red-500 shrink-0" />
                  <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Cancelled</span>
                  <span class="text-xs text-gray-700 dark:text-dark-text-secondary">{formatDateTime(task.cancelled_at)}</span>
                </div>
              {/if}
              {#if task.checked_out_at}
                <div class="px-3 py-2 flex items-center gap-2">
                  <Clock size={12} class="text-yellow-500 shrink-0" />
                  <span class="text-xs text-gray-500 dark:text-dark-text-muted w-20 shrink-0">Checked out</span>
                  <span class="text-xs text-gray-700 dark:text-dark-text-secondary">{formatDateTime(task.checked_out_at)}</span>
                </div>
              {/if}
            </div>
          </div>

          <!-- Actions -->
          {#if task.organization_id}
            <div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
              <div class="px-3 py-2 border-b border-gray-100 dark:border-dark-border">
                <span class="text-[10px] font-medium text-gray-500 dark:text-dark-text-muted uppercase tracking-wider">Actions</span>
              </div>
              <div class="px-3 py-2 space-y-2">
                {#if delegationActive}
                  {@const activeAgent = activeDeepest ? agentDisplayName(activeDeepest.agent_id) : ''}
                  {@const isDescendant = !!activeDeepest && activeDeepest.task_id !== task.id}
                  <div class="flex items-center gap-2 px-2 py-1.5 bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-900/40 text-yellow-800 dark:text-yellow-400 text-[11px]">
                    <Loader2 size={12} class="animate-spin shrink-0" />
                    <div class="flex-1 min-w-0">
                      <div class="font-medium truncate" title={activeAgent}>
                        {#if activeAgent}
                          {activeAgent} working
                        {:else}
                          Agent working
                        {/if}
                      </div>
                      <div class="text-[10px] text-yellow-600 dark:text-yellow-500 flex items-center gap-1.5">
                        {#if delegationDuration}
                          <span>{delegationDuration} elapsed</span>
                        {/if}
                        {#if isDescendant && activeDeepest}
                          <span class="opacity-60">·</span>
                          <a
                            href="#/tasks/{activeDeepest.task_id}"
                            class="underline-offset-2 hover:underline truncate"
                            title="Jump to in-flight subtask"
                          >
                            in subtask
                          </a>
                        {/if}
                      </div>
                    </div>
                    <button
                      onclick={handleCancelDelegation}
                      disabled={cancelling || !delegationRootActive}
                      class="px-2 py-1 text-[10px] font-medium bg-red-600 text-white hover:bg-red-700 transition-colors disabled:opacity-50 shrink-0"
                      title={delegationRootActive ? 'Cancel delegation' : 'Cancellation must be initiated on the root task'}
                    >
                      {cancelling ? '...' : 'Stop'}
                    </button>
                  </div>
                {/if}

                <button
                  onclick={handleProcess}
                  disabled={processing || delegationActive}
                  class="flex items-center gap-1.5 text-xs text-green-700 dark:text-green-400 hover:text-green-800 dark:hover:text-green-300 transition-colors disabled:opacity-50"
                >
                  <Play size={12} />
                  {processing ? 'Processing...' : 'Process (Start Delegation)'}
                </button>

                <button
                  onclick={handleOpenChat}
                  disabled={openingChat}
                  class="flex items-center gap-1.5 text-xs text-blue-700 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-300 transition-colors disabled:opacity-50"
                >
                  <Activity size={12} />
                  {openingChat ? 'Opening...' : 'Open Chat'}
                </button>

                {#if task.status === 'completed' || task.status === 'in_review' || task.status === 'done'}
                  {#if showRevisionForm}
                    <div class="space-y-2 pt-1">
                      <textarea
                        bind:value={revisionFeedback}
                        rows="3"
                        placeholder="Describe what needs to change..."
                        class="w-full border border-gray-200 dark:border-dark-border-subtle px-2 py-1.5 text-xs bg-transparent dark:bg-dark-elevated dark:text-dark-text focus:outline-none focus:border-gray-400 dark:focus:border-accent/50 resize-y transition-colors"
                      ></textarea>
                      <div class="flex gap-1.5">
                        <button
                          onclick={handleRequestRevision}
                          disabled={requestingRevision || !revisionFeedback.trim()}
                          class="flex-1 flex items-center justify-center gap-1.5 px-2 py-1.5 text-xs bg-orange-600 text-white hover:bg-orange-700 transition-colors disabled:opacity-50"
                        >
                          <RotateCcw size={11} />
                          {requestingRevision ? 'Sending...' : 'Send & Reprocess'}
                        </button>
                        <button
                          onclick={() => { showRevisionForm = false; revisionFeedback = ''; }}
                          class="px-2 py-1.5 text-xs border border-gray-200 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated text-gray-500 dark:text-dark-text-muted transition-colors"
                        >
                          Cancel
                        </button>
                      </div>
                    </div>
                  {:else}
                    <button
                      onclick={() => (showRevisionForm = true)}
                      class="flex items-center gap-1.5 text-xs text-orange-600 dark:text-orange-400 hover:text-orange-700 dark:hover:text-orange-300 transition-colors"
                    >
                      <RotateCcw size={12} />
                      Request Revision
                    </button>
                  {/if}
                {/if}
              </div>
            </div>
          {/if}

          <!-- Danger zone -->
          <div class="border border-red-200 dark:border-red-900/30 bg-white dark:bg-dark-surface">
            <div class="px-3 py-2 border-b border-red-100 dark:border-red-900/20">
              <span class="text-[10px] font-medium text-red-500 dark:text-red-400 uppercase tracking-wider">Danger Zone</span>
            </div>
            <div class="px-3 py-2">
              {#if deleteConfirm}
                <div class="flex items-center gap-2">
                  <span class="text-xs text-red-600 dark:text-red-400">Delete this task?</span>
                  <button
                    onclick={handleDelete}
                    class="px-2 py-1 text-xs bg-red-600 text-white hover:bg-red-700 transition-colors"
                  >
                    Confirm
                  </button>
                  <button
                    onclick={() => (deleteConfirm = false)}
                    class="px-2 py-1 text-xs border border-gray-300 dark:border-dark-border-subtle hover:bg-gray-50 dark:hover:bg-dark-elevated transition-colors"
                  >
                    Cancel
                  </button>
                </div>
              {:else}
                <button
                  onclick={() => (deleteConfirm = true)}
                  class="flex items-center gap-1.5 text-xs text-red-600 dark:text-red-400 hover:text-red-700 dark:hover:text-red-300 transition-colors"
                >
                  <Trash2 size={12} />
                  Delete task
                </button>
              {/if}
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
{/if}

<style>
  @reference "../style/global.css";

  .markdown-body :global(p) {
    @apply mb-2 last:mb-0;
  }
  .markdown-body :global(a) {
    @apply underline underline-offset-2 hover:opacity-80;
  }
  .markdown-body :global(strong) {
    @apply font-semibold;
  }
  .markdown-body :global(code) {
    @apply font-mono text-[0.85em] bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 rounded;
  }
  .markdown-body :global(pre) {
    @apply bg-gray-100 dark:bg-dark-elevated px-3 py-2 my-2 overflow-x-auto text-[0.85em] rounded;
  }
  .markdown-body :global(pre code) {
    @apply bg-transparent px-0 py-0;
  }
  .markdown-body :global(ul) {
    @apply list-disc pl-5 mb-2;
  }
  .markdown-body :global(ol) {
    @apply list-decimal pl-5 mb-2;
  }
  .markdown-body :global(li) {
    @apply mb-0.5;
  }
  .markdown-body :global(blockquote) {
    @apply border-l-2 border-gray-300 dark:border-dark-border pl-3 my-2 text-gray-600 dark:text-dark-text-secondary;
  }
  .markdown-body :global(h1) {
    @apply text-lg font-semibold mb-2;
  }
  .markdown-body :global(h2) {
    @apply text-base font-semibold mb-1.5;
  }
  .markdown-body :global(h3) {
    @apply text-sm font-semibold mb-1;
  }
  .markdown-body :global(h4),
  .markdown-body :global(h5),
  .markdown-body :global(h6) {
    @apply text-sm font-medium mb-1;
  }
  .markdown-body :global(hr) {
    @apply border-t border-gray-200 dark:border-dark-border my-3;
  }
  .markdown-body :global(img) {
    @apply max-w-full my-2;
  }
  .markdown-body :global(table) {
    @apply w-full border-collapse my-2 text-sm;
  }
  .markdown-body :global(th),
  .markdown-body :global(td) {
    @apply border border-gray-200 dark:border-dark-border px-2 py-1 text-left;
  }
  .markdown-body :global(th) {
    @apply bg-gray-50 dark:bg-dark-elevated font-medium;
  }

  /* Sub-task tree: striped rows with hover highlight */
  :global(.subtask-row:nth-child(odd) > .subtask-inner) {
    @apply bg-gray-50/70 dark:bg-dark-elevated/30;
  }
  :global(.subtask-row:nth-child(even) > .subtask-inner) {
    @apply bg-white dark:bg-dark-surface;
  }
  :global(.subtask-row > .subtask-inner:hover) {
    @apply bg-gray-100 dark:bg-dark-elevated/70;
  }
  /* Live row: subtask has a running delegation goroutine right now.
     The pulse + bg tint flag the in-flight node in a multi-level tree
     so the user can tell at a glance which agent is actually working. */
  :global(.subtask-row-live > .subtask-inner),
  :global(.subtask-row-live:nth-child(odd) > .subtask-inner),
  :global(.subtask-row-live:nth-child(even) > .subtask-inner) {
    @apply bg-green-50 dark:bg-green-900/20;
    border-left: 2px solid rgb(34 197 94);
  }
  :global(.subtask-row-live > .subtask-inner:hover) {
    @apply bg-green-100 dark:bg-green-900/30;
  }
</style>
