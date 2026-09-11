<script lang="ts">
  import { onMount, tick, untrack } from 'svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import {
    getTaskBoard,
    isTaskBoardConflict,
    resetTaskBoard,
    saveTaskBoard,
    taskBoardErrorMessage,
  } from '@/lib/api/task-board';
  import {
    TASK_BOARD_COLORS,
    TASK_BOARD_COLOR_LABELS,
    TASK_BOARD_MAX_COLUMNS,
    TASK_BOARD_MAX_LABEL_LENGTH,
    TASK_BOARD_STATUSES,
    cloneColumns,
    columnDotClass,
    columnProblem,
    dropStatus,
    moveItem,
    statusOwnerIndex,
    taskStatusText,
    uncoveredStatuses,
    validateTaskBoardColumns,
    type TaskBoard,
    type TaskBoardColumn,
  } from '@/lib/helper/task-board';
  import {
    ArrowDown, ArrowLeft, ArrowRight, ArrowUp, EyeOff, Plus, RotateCcw, Save, Trash2, TriangleAlert, X,
  } from 'lucide-svelte';

  interface Props {
    /** The board as last read. Its `version` is what a save echoes back. */
    board: TaskBoard;
    /** Tasks per status in the loaded set. Drives the removal warning. */
    statusCounts?: Record<string, number>;
    onclose: () => void;
    /** Called with the server's answer after a successful save or reset. */
    onsaved: (board: TaskBoard) => void;
  }

  let { board, statusCounts = {}, onclose, onsaved }: Props = $props();

  // The draft is a snapshot on purpose: it is detached so Cancel is a genuine
  // no-op, and it must not be rewritten under the reader's hands when the page
  // refreshes the board. `untrack` states that intent rather than leaving it to
  // be inferred. The editor is mounted per open, so the snapshot is current.
  let draft = $state<TaskBoardColumn[]>(untrack(() => cloneColumns(board.columns)));
  let version = $state(untrack(() => board.version));
  let available = $state<string[]>(untrack(() =>
    board.available_statuses?.length ? [...board.available_statuses] : [...TASK_BOARD_STATUSES],
  ));

  let saving = $state(false);
  let error = $state('');
  let conflict = $state(false);
  let removeIndex = $state<number | null>(null);
  let resetConfirm = $state(false);
  let dialog = $state<HTMLDivElement | null>(null);

  // ─── Derived state ───

  let problems = $derived(draft.map((_, i) => columnProblem(draft, i)));
  let blocking = $derived(validateTaskBoardColumns(draft));
  let uncovered = $derived(uncoveredStatuses(draft, available));
  let hiddenCount = $derived(uncovered.reduce((total, status) => total + (statusCounts[status] || 0), 0));

  const columnName = (index: number) => (draft[index]?.label || '').trim() || `column ${index + 1}`;
  const columnTaskCount = (index: number) =>
    (draft[index]?.statuses || []).reduce((total, status) => total + (statusCounts[status] || 0), 0);

  // ─── Focus ───
  // Reordering is done with buttons, not drag handles, so it is reachable from
  // the keyboard. Focus follows the moved item, otherwise a second press would
  // land on whatever slid into the vacated slot.

  function focusById(...ids: string[]) {
    for (const id of ids) {
      const node = document.getElementById(id) as HTMLElement | null;
      if (node && !(node as HTMLButtonElement).disabled) {
        node.focus();
        return;
      }
    }
  }

  // ─── Column edits ───

  async function addColumn() {
    if (draft.length >= TASK_BOARD_MAX_COLUMNS) return;
    draft = [...draft, { id: '', label: '', color: 'gray', statuses: [] }];
    error = '';
    await tick();
    focusById(`board-col-label-${draft.length - 1}`);
  }

  function requestRemove(index: number) {
    if (columnTaskCount(index) > 0) {
      removeIndex = index;
      return;
    }
    removeColumn(index);
  }

  async function removeColumn(index: number) {
    draft = draft.filter((_, i) => i !== index);
    removeIndex = null;
    error = '';
    await tick();
    focusById('board-add-column');
  }

  async function moveColumn(index: number, delta: number, key: 'left' | 'right') {
    const to = index + delta;
    if (to < 0 || to >= draft.length) return;
    draft = moveItem(draft, index, to);
    removeIndex = null;
    await tick();
    focusById(`board-col-${key}-${to}`, `board-col-${key === 'left' ? 'right' : 'left'}-${to}`);
  }

  function setColor(index: number, color: string) {
    draft[index] = { ...draft[index], color };
  }

  // ─── Status assignment ───
  // A status can only live in one column, so assigning it anywhere removes it
  // everywhere else first. The conflict is impossible to create rather than
  // reported after the fact.

  function assignStatus(index: number, status: string) {
    if (!status) return;
    draft = draft.map((column, i) => {
      const statuses = (column.statuses || []).filter(s => s !== status);
      return i === index ? { ...column, statuses: [...statuses, status] } : { ...column, statuses };
    });
    error = '';
  }

  function unassignStatus(index: number, status: string) {
    draft[index] = { ...draft[index], statuses: draft[index].statuses.filter(s => s !== status) };
  }

  async function moveStatus(index: number, from: number, delta: number, key: 'up' | 'down') {
    const statuses = draft[index]?.statuses || [];
    const to = from + delta;
    if (to < 0 || to >= statuses.length) return;
    draft[index] = { ...draft[index], statuses: moveItem(statuses, from, to) };
    await tick();
    focusById(`board-status-${key}-${index}-${to}`, `board-status-${key === 'up' ? 'down' : 'up'}-${index}-${to}`);
  }

  // ─── Persistence ───

  async function save() {
    if (blocking) {
      error = blocking;
      return;
    }
    saving = true;
    error = '';
    try {
      const saved = await saveTaskBoard(version, draft);
      addToast('Board columns saved');
      onsaved(saved);
      onclose();
    } catch (e) {
      conflict = isTaskBoardConflict(e);
      error = taskBoardErrorMessage(e);
    } finally {
      saving = false;
    }
  }

  async function reset() {
    saving = true;
    error = '';
    try {
      const fresh = await resetTaskBoard();
      addToast('Board columns reset to the default layout');
      onsaved(fresh);
      onclose();
    } catch (e) {
      conflict = isTaskBoardConflict(e);
      error = taskBoardErrorMessage(e, 'Could not reset the board columns.');
    } finally {
      saving = false;
      resetConfirm = false;
    }
  }

  /** Only on an explicit click: a 409 must never silently overwrite an edit. */
  async function reloadFromServer() {
    saving = true;
    try {
      const fresh = await getTaskBoard();
      draft = cloneColumns(fresh.columns);
      version = fresh.version;
      available = fresh.available_statuses?.length ? [...fresh.available_statuses] : [...TASK_BOARD_STATUSES];
      conflict = false;
      error = '';
      onsaved(fresh);
    } catch (e) {
      error = taskBoardErrorMessage(e, 'Could not reload the board columns.');
    } finally {
      saving = false;
    }
  }

  // ─── Dialog plumbing ───

  onMount(() => {
    const opener = document.activeElement as HTMLElement | null;
    dialog?.focus();
    return () => opener?.focus?.();
  });

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.stopPropagation();
      if (removeIndex !== null) { removeIndex = null; return; }
      if (resetConfirm) { resetConfirm = false; return; }
      onclose();
      return;
    }
    if (e.key !== 'Tab' || !dialog) return;
    const focusable = dialog.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])',
    );
    if (!focusable.length) return;
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault();
      first.focus();
    }
  }

  // Focus has to stay visible: reordering and status assignment are driven
  // from these buttons, so a keyboard user needs to see where they are.
  const focusRing =
    'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 dark:focus-visible:outline-accent';

  const iconButton =
    `p-1 text-gray-500 dark:text-dark-text-secondary hover:text-gray-900 dark:hover:text-dark-text hover:bg-gray-100 dark:hover:bg-dark-elevated ${focusRing} focus-visible:outline-offset-1 disabled:opacity-40 disabled:cursor-not-allowed transition-colors motion-reduce:transition-none`;

  const actionButton = `px-3 py-1.5 text-xs font-medium ${focusRing} transition-colors motion-reduce:transition-none`;
  const primaryButton =
    `${actionButton} flex items-center gap-1.5 bg-gray-900 text-white hover:bg-gray-800 dark:bg-accent dark:text-gray-950 dark:hover:bg-accent-hover disabled:opacity-50 disabled:cursor-not-allowed`;
  const secondaryButton =
    `${actionButton} border border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated disabled:opacity-40 disabled:cursor-not-allowed`;
</script>

<div class="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-gray-900/50 dark:bg-black/70 p-4">
  <div
    bind:this={dialog}
    role="dialog"
    aria-modal="true"
    aria-labelledby="board-editor-title"
    aria-describedby="board-editor-intro"
    tabindex="-1"
    onkeydown={onKeydown}
    class="w-full max-w-5xl my-6 bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border shadow-xl outline-none"
  >
    <!-- Header -->
    <div class="flex items-start justify-between gap-4 px-5 py-4 border-b border-gray-200 dark:border-dark-border">
      <div>
        <h2 id="board-editor-title" class="text-sm font-semibold text-gray-900 dark:text-dark-text">Board columns</h2>
        <p id="board-editor-intro" class="mt-1 text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary max-w-2xl">
          Each column collects one or more task statuses. A status belongs to one column at a time, and dragging a
          card into a column sets the task to that column's first status.
        </p>
      </div>
      <button
        onclick={onclose}
        aria-label="Close the board column editor"
        class="{iconButton} shrink-0"
      >
        <X size={16} />
      </button>
    </div>

    <!-- Conflict banner -->
    {#if conflict}
      <div class="flex flex-wrap items-start gap-3 px-5 py-3 border-b border-amber-200 dark:border-amber-900/50 bg-amber-50 dark:bg-amber-950/40">
        <TriangleAlert size={16} class="mt-0.5 shrink-0 text-amber-700 dark:text-amber-400" />
        <div class="flex-1 min-w-[20rem]">
          <p class="text-xs font-medium text-amber-900 dark:text-amber-200">The board was changed by someone else.</p>
          <p class="mt-1 text-xs leading-relaxed text-amber-800 dark:text-amber-200/90">
            Your edit was not saved, so nothing of theirs was overwritten. Reloading replaces this draft with the
            current board; keep editing if you would rather copy your changes across by hand first.
          </p>
        </div>
        <div class="flex items-center gap-2">
          <button
            onclick={reloadFromServer}
            disabled={saving}
            class="px-2.5 py-1.5 text-xs font-medium bg-amber-800 text-white hover:bg-amber-900 dark:bg-amber-700 dark:hover:bg-amber-600 disabled:opacity-50 transition-colors motion-reduce:transition-none {focusRing}"
          >
            Reload the board
          </button>
          <button
            onclick={() => (conflict = false)}
            class="px-2.5 py-1.5 text-xs font-medium border border-amber-300 dark:border-amber-800 text-amber-900 dark:text-amber-200 hover:bg-amber-100 dark:hover:bg-amber-900/40 transition-colors motion-reduce:transition-none {focusRing}"
          >
            Keep editing
          </button>
        </div>
      </div>
    {/if}

    <!-- Columns -->
    <div class="flex gap-3 overflow-x-auto px-5 py-4">
      {#each draft as column, i}
        <section
          aria-label="Column {i + 1}: {columnName(i)}"
          class={[
            'flex flex-col w-72 shrink-0 border bg-gray-50 dark:bg-dark-base',
            problems[i] ? 'border-red-300 dark:border-red-900' : 'border-gray-200 dark:border-dark-border',
          ]}
        >
          <!-- Name and column actions -->
          <div class="flex items-center gap-1 px-2 py-2 border-b border-gray-200 dark:border-dark-border">
            <span class="w-2.5 h-2.5 shrink-0 {columnDotClass(column.color)}"></span>
            <input
              id="board-col-label-{i}"
              type="text"
              bind:value={column.label}
              maxlength={TASK_BOARD_MAX_LABEL_LENGTH}
              placeholder="Column name"
              aria-label="Name of column {i + 1}"
              class="flex-1 min-w-0 bg-transparent px-1 py-0.5 text-sm font-medium text-gray-900 dark:text-dark-text border border-transparent hover:border-gray-300 dark:hover:border-dark-border-subtle focus:outline-none focus:border-gray-900 dark:focus:border-accent placeholder:text-gray-400 dark:placeholder:text-dark-text-muted"
            />
            <button
              id="board-col-left-{i}"
              onclick={() => moveColumn(i, -1, 'left')}
              disabled={i === 0}
              aria-label="Move {columnName(i)} left"
              class={iconButton}
            >
              <ArrowLeft size={14} />
            </button>
            <button
              id="board-col-right-{i}"
              onclick={() => moveColumn(i, 1, 'right')}
              disabled={i === draft.length - 1}
              aria-label="Move {columnName(i)} right"
              class={iconButton}
            >
              <ArrowRight size={14} />
            </button>
            <button
              onclick={() => requestRemove(i)}
              aria-label="Remove {columnName(i)}"
              class="{iconButton} hover:text-red-600 dark:hover:text-red-400"
            >
              <Trash2 size={14} />
            </button>
          </div>

          <!-- Removal confirmation -->
          {#if removeIndex === i}
            <div class="px-3 py-2.5 border-b border-red-200 dark:border-red-900/60 bg-red-50 dark:bg-red-950/40">
              <p class="text-xs leading-relaxed text-red-800 dark:text-red-200">
                {columnTaskCount(i) === 1 ? '1 task is' : `${columnTaskCount(i)} tasks are`} in
                "{columnName(i)}". Removing it keeps their status but takes them off the board unless another column
                collects it.
              </p>
              <div class="flex gap-2 mt-2">
                <button
                  onclick={() => removeColumn(i)}
                  class="px-2 py-1 text-xs font-medium bg-red-600 text-white hover:bg-red-700 transition-colors motion-reduce:transition-none {focusRing}"
                >
                  Remove the column
                </button>
                <button
                  onclick={() => (removeIndex = null)}
                  class="px-2 py-1 text-xs font-medium border border-red-300 dark:border-red-900 text-red-800 dark:text-red-200 hover:bg-red-100 dark:hover:bg-red-900/40 transition-colors motion-reduce:transition-none {focusRing}"
                >
                  Keep it
                </button>
              </div>
            </div>
          {/if}

          <!-- Colour -->
          <fieldset class="px-3 py-2 border-b border-gray-200 dark:border-dark-border">
            <legend class="sr-only">Colour for {columnName(i)}</legend>
            <div class="flex items-center gap-2">
              {#each TASK_BOARD_COLORS as color}
                <label class="flex cursor-pointer">
                  <input
                    type="radio"
                    name="board-color-{i}"
                    value={color}
                    checked={(column.color || 'gray') === color}
                    onchange={() => setColor(i, color)}
                    class="peer sr-only"
                  />
                  <span
                    aria-hidden="true"
                    class="w-5 h-5 {columnDotClass(color)} ring-offset-2 ring-offset-gray-50 dark:ring-offset-dark-base peer-checked:ring-2 peer-checked:ring-gray-900 dark:peer-checked:ring-dark-text peer-focus-visible:ring-2 peer-focus-visible:ring-gray-900 dark:peer-focus-visible:ring-accent"
                  ></span>
                  <span class="sr-only">{TASK_BOARD_COLOR_LABELS[color]}</span>
                </label>
              {/each}
            </div>
          </fieldset>

          <!-- Statuses -->
          <div class="flex-1 px-3 py-2.5">
            {#if column.statuses.length}
              <ol class="space-y-1">
                {#each column.statuses as status, si (status)}
                  <li class="flex items-center gap-1 pl-2 pr-1 py-1 bg-white dark:bg-dark-surface border border-gray-200 dark:border-dark-border">
                    <span class="flex-1 min-w-0 truncate text-xs text-gray-900 dark:text-dark-text">
                      {taskStatusText(status)}
                    </span>
                    <span class="text-[11px] font-mono tabular-nums text-gray-500 dark:text-dark-text-secondary">
                      {statusCounts[status] || 0}<span class="sr-only"> tasks loaded</span>
                    </span>
                    <button
                      id="board-status-up-{i}-{si}"
                      onclick={() => moveStatus(i, si, -1, 'up')}
                      disabled={si === 0}
                      aria-label="Move {taskStatusText(status)} up in {columnName(i)}"
                      class={iconButton}
                    >
                      <ArrowUp size={13} />
                    </button>
                    <button
                      id="board-status-down-{i}-{si}"
                      onclick={() => moveStatus(i, si, 1, 'down')}
                      disabled={si === column.statuses.length - 1}
                      aria-label="Move {taskStatusText(status)} down in {columnName(i)}"
                      class={iconButton}
                    >
                      <ArrowDown size={13} />
                    </button>
                    <button
                      onclick={() => unassignStatus(i, status)}
                      aria-label="Take {taskStatusText(status)} off {columnName(i)}"
                      class="{iconButton} hover:text-red-600 dark:hover:text-red-400"
                    >
                      <X size={13} />
                    </button>
                  </li>
                {/each}
              </ol>
              <p class="mt-2 text-[11px] leading-relaxed text-gray-600 dark:text-dark-text-secondary">
                A card dropped here becomes <strong class="font-medium text-gray-900 dark:text-dark-text">{taskStatusText(dropStatus(column))}</strong>.
              </p>
            {:else}
              <p class="text-xs leading-relaxed text-gray-600 dark:text-dark-text-secondary">
                No statuses yet, so nothing would appear in this column.
              </p>
            {/if}

            {#if problems[i]}
              <p role="alert" class="mt-2 text-xs leading-relaxed text-red-700 dark:text-red-300">{problems[i]}</p>
            {/if}
          </div>

          <!-- Assign a status -->
          <div class="px-3 py-2.5 border-t border-gray-200 dark:border-dark-border">
            <label class="sr-only" for="board-add-status-{i}">Add a status to {columnName(i)}</label>
            <select
              id="board-add-status-{i}"
              onchange={(e) => { assignStatus(i, e.currentTarget.value); e.currentTarget.value = ''; }}
              class="w-full border border-gray-300 dark:border-dark-border-subtle px-2 py-1.5 text-xs text-gray-900 dark:bg-dark-elevated dark:text-dark-text focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-gray-900 dark:focus-visible:outline-accent"
            >
              <option value="">Add a status…</option>
              {#each available.filter(s => !column.statuses.includes(s)) as status}
                {@const owner = statusOwnerIndex(draft, status, i)}
                <option value={status}>{taskStatusText(status)}{owner >= 0 ? ` (currently in ${columnName(owner)}, will move)` : ''}</option>
              {/each}
            </select>
          </div>
        </section>
      {/each}

      <!-- Add a column -->
      <div class="shrink-0 flex items-start">
        <button
          id="board-add-column"
          onclick={addColumn}
          disabled={draft.length >= TASK_BOARD_MAX_COLUMNS}
          class="flex items-center gap-1.5 px-3 py-2 text-xs font-medium border border-dashed border-gray-300 dark:border-dark-border-subtle text-gray-700 dark:text-dark-text-secondary hover:border-gray-900 dark:hover:border-accent hover:text-gray-900 dark:hover:text-dark-text disabled:opacity-40 disabled:cursor-not-allowed transition-colors motion-reduce:transition-none {focusRing}"
        >
          <Plus size={13} />
          {draft.length >= TASK_BOARD_MAX_COLUMNS ? `${TASK_BOARD_MAX_COLUMNS} columns is the maximum` : 'Add a column'}
        </button>
      </div>
    </div>

    <!-- What this layout hides -->
    {#if uncovered.length}
      <div class="flex items-start gap-2.5 mx-5 mb-4 px-3 py-2.5 border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
        <EyeOff size={14} class="mt-0.5 shrink-0 text-gray-600 dark:text-dark-text-secondary" />
        <p class="text-xs leading-relaxed text-gray-700 dark:text-dark-text-secondary">
          Not on the board: <span class="text-gray-900 dark:text-dark-text">{uncovered.map(taskStatusText).join(', ')}</span>.
          {#if hiddenCount > 0}
            {hiddenCount === 1 ? '1 loaded task is' : `${hiddenCount} loaded tasks are`} in {uncovered.length === 1 ? 'it' : 'those statuses'} and would not be shown.
          {:else}
            No loaded task is in {uncovered.length === 1 ? 'it' : 'those statuses'} right now.
          {/if}
        </p>
      </div>
    {/if}

    <!-- Footer -->
    <div class="px-5 py-3.5 border-t border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
      {#if error}
        <p role="alert" class="mb-3 text-xs leading-relaxed text-red-700 dark:text-red-300">{error}</p>
      {:else if blocking}
        <p class="mb-3 text-xs leading-relaxed text-gray-700 dark:text-dark-text-secondary">{blocking}</p>
      {/if}

      {#if resetConfirm}
        <div class="mb-3 px-3 py-2.5 border border-gray-300 dark:border-dark-border-subtle bg-white dark:bg-dark-surface">
          <p class="text-xs leading-relaxed text-gray-700 dark:text-dark-text-secondary">
            Reset discards the saved layout for everyone in this workspace and returns to the four default columns.
          </p>
          <div class="flex gap-2 mt-2">
            <button
              onclick={reset}
              disabled={saving}
              class="{primaryButton}"
            >
              Reset to the default
            </button>
            <button
              onclick={() => (resetConfirm = false)}
              class="{secondaryButton}"
            >
              Keep my layout
            </button>
          </div>
        </div>
      {/if}

      <div class="flex flex-wrap items-center justify-between gap-3">
        <button
          onclick={() => (resetConfirm = true)}
          disabled={saving || board.default}
          title={board.default ? 'This workspace is already using the default columns' : ''}
          class="{secondaryButton} flex items-center gap-1.5"
        >
          <RotateCcw size={13} />
          Reset to default
        </button>

        <div class="flex items-center gap-2">
          <button
            onclick={onclose}
            class="{secondaryButton}"
          >
            Cancel
          </button>
          <button
            onclick={save}
            disabled={saving || !!blocking}
            class="{primaryButton}"
          >
            <Save size={13} />
            {saving ? 'Saving…' : 'Save columns'}
          </button>
        </div>
      </div>
    </div>
  </div>
</div>
