// Kanban board layout: pure logic shared by the board, the column editor and
// `tests/task-board.test.mjs`.
//
// The test harness compiles exactly one `.ts` file to a data URL, so this
// module must not import anything at runtime. Everything below is local.
//
// Two rules keep the board honest, and both are mirrored from the server so a
// save is never a surprise:
//
//   - A status belongs to at most one column. Otherwise the same task would be
//     drawn twice and dragging it would be ambiguous.
//   - A status may be left off the board entirely, but the tasks that carry it
//     are then invisible. `hiddenTaskSummary` exists so the board can say so
//     out loud instead of quietly dropping work.

// ─── Vocabulary ───

/**
 * The canonical task statuses, in board order. The server echoes the same list
 * as `available_statuses`, which callers should prefer; this copy is the
 * fallback for a board that could not be fetched, and the harness-imposed
 * duplicate of `TASK_STATUSES` in `lib/api/tasks.ts` (the test asserts the two
 * stay identical).
 */
export const TASK_BOARD_STATUSES = [
  'backlog', 'todo', 'in_progress', 'in_review', 'blocked', 'done', 'cancelled',
];

export const TASK_BOARD_STATUS_LABELS: Record<string, string> = {
  backlog: 'Backlog',
  todo: 'To Do',
  in_progress: 'In Progress',
  in_review: 'In Review',
  blocked: 'Blocked',
  done: 'Done',
  cancelled: 'Cancelled',
};

/** Human text for a status, including one the vocabulary no longer contains. */
export function taskStatusText(status: string): string {
  const key = typeof status === 'string' ? status : '';
  return TASK_BOARD_STATUS_LABELS[key] || key.replace(/_/g, ' ') || 'unknown';
}

/** Server limits, mirrored so the editor can refuse before the request does. */
export const TASK_BOARD_MAX_COLUMNS = 12;
export const TASK_BOARD_MAX_LABEL_LENGTH = 40;

// ─── Types ───

export interface TaskBoardColumn {
  /** Stable slug. May be omitted on write; the server derives it from `label`. */
  id: string;
  label: string;
  /** Free-form token. The board only renders the six in `TASK_BOARD_COLORS`. */
  color?: string;
  /** One or more canonical statuses. `statuses[0]` is what a drop applies. */
  statuses: string[];
}

export interface TaskBoard {
  workspace_id: string;
  /** `0` when nothing is stored. Echoed back on write to detect a concurrent edit. */
  version: number;
  columns: TaskBoardColumn[];
  updated_at?: string;
  updated_by?: string;
  /** `true` when these are the shipped columns and nothing has been saved yet. */
  default: boolean;
  /** Canonical statuses no column collects. Absent when the board covers all of them. */
  uncovered_statuses?: string[];
  /** The full vocabulary, in board order. */
  available_statuses: string[];
}

// ─── Colours ───

export const TASK_BOARD_COLORS = ['blue', 'yellow', 'red', 'green', 'gray', 'purple'] as const;

export type TaskBoardColor = (typeof TASK_BOARD_COLORS)[number];

export const TASK_BOARD_COLOR_LABELS: Record<string, string> = {
  blue: 'Blue', yellow: 'Yellow', red: 'Red', green: 'Green', gray: 'Gray', purple: 'Purple',
};

const COLOR_DOT: Record<string, string> = {
  blue: 'bg-blue-500 dark:bg-blue-400',
  yellow: 'bg-yellow-500 dark:bg-yellow-400',
  red: 'bg-red-500 dark:bg-red-400',
  green: 'bg-green-600 dark:bg-green-400',
  gray: 'bg-gray-400 dark:bg-gray-500',
  purple: 'bg-purple-500 dark:bg-purple-400',
};

/** Swatch classes for a colour token. An unknown token reads as gray. */
export function columnDotClass(color: string | undefined): string {
  return COLOR_DOT[(color || '').trim()] || COLOR_DOT.gray;
}

// ─── Defaults ───

/** The layout a workspace gets before it customises anything. */
export function defaultTaskBoardColumns(): TaskBoardColumn[] {
  return [
    { id: 'todo', label: 'To Do', color: 'blue', statuses: ['backlog', 'todo'] },
    { id: 'in_progress', label: 'In Progress', color: 'yellow', statuses: ['in_progress', 'in_review'] },
    { id: 'blocked', label: 'Blocked', color: 'red', statuses: ['blocked'] },
    { id: 'done', label: 'Done', color: 'green', statuses: ['done', 'cancelled'] },
  ];
}

/** A board to render when the fetch failed: stale beats absent. */
export function defaultTaskBoard(): TaskBoard {
  return {
    workspace_id: '',
    version: 0,
    columns: defaultTaskBoardColumns(),
    default: true,
    uncovered_statuses: [],
    available_statuses: [...TASK_BOARD_STATUSES],
  };
}

/** A detached copy, so an editor can be cancelled without touching the board. */
export function cloneColumns(columns: TaskBoardColumn[] | undefined): TaskBoardColumn[] {
  return (columns || []).map(column => ({
    id: column?.id || '',
    label: column?.label || '',
    color: column?.color || '',
    statuses: [...(column?.statuses || [])],
  }));
}

// ─── Lookup ───

/**
 * status → column id. The first column to claim a status wins, which matches
 * how the server resolves a layout that slipped through with a duplicate.
 */
export function statusColumnLookup(columns: TaskBoardColumn[] | undefined): Record<string, string> {
  const lookup: Record<string, string> = {};
  for (const column of columns || []) {
    for (const status of column?.statuses || []) {
      if (!(status in lookup)) lookup[status] = column.id;
    }
  }
  return lookup;
}

/** `''` when no column collects the status, meaning the task is not drawn. */
export function columnIdForStatus(columns: TaskBoardColumn[] | undefined, status: string): string {
  return statusColumnLookup(columns)[status] || '';
}

/** The status a card takes when dropped into this column. */
export function dropStatus(column: TaskBoardColumn | undefined | null): string {
  return column?.statuses?.[0] || '';
}

/** Index of the column holding `status`, ignoring one column. `-1` when free. */
export function statusOwnerIndex(
  columns: TaskBoardColumn[] | undefined,
  status: string,
  ignoreIndex = -1,
): number {
  const list = columns || [];
  for (let i = 0; i < list.length; i += 1) {
    if (i === ignoreIndex) continue;
    if ((list[i]?.statuses || []).includes(status)) return i;
  }
  return -1;
}

// ─── Conflicts and gaps ───

export interface TaskBoardStatusConflict {
  status: string;
  /** Labels of the columns claiming it, in board order. */
  columns: string[];
}

/**
 * Statuses claimed by more than one column. A repeat inside a single column is
 * not a conflict: the server drops it silently.
 */
export function duplicateStatuses(columns: TaskBoardColumn[] | undefined): TaskBoardStatusConflict[] {
  const list = columns || [];
  const owners = new Map<string, number[]>();
  list.forEach((column, index) => {
    const seen = new Set<string>();
    for (const status of column?.statuses || []) {
      if (seen.has(status)) continue;
      seen.add(status);
      owners.set(status, [...(owners.get(status) || []), index]);
    }
  });
  const conflicts: TaskBoardStatusConflict[] = [];
  for (const [status, indexes] of owners) {
    if (indexes.length < 2) continue;
    conflicts.push({ status, columns: indexes.map(i => list[i]?.label || `Column ${i + 1}`) });
  }
  return conflicts;
}

/** Canonical statuses no column collects, in vocabulary order. */
export function uncoveredStatuses(
  columns: TaskBoardColumn[] | undefined,
  available: string[] = TASK_BOARD_STATUSES,
): string[] {
  const covered = statusColumnLookup(columns);
  return (available || []).filter(status => !(status in covered));
}

export interface HiddenTaskSummary {
  /** How many of the supplied tasks no column would draw. */
  count: number;
  /** Their distinct statuses, vocabulary order first, then anything unknown. */
  statuses: string[];
}

/**
 * What this layout hides from the tasks actually loaded. A status left off the
 * board only matters when work is sitting in it, so the count comes from the
 * tasks rather than from the vocabulary.
 */
export function hiddenTaskSummary(
  tasks: { status: string }[] | undefined,
  columns: TaskBoardColumn[] | undefined,
): HiddenTaskSummary {
  const lookup = statusColumnLookup(columns);
  const statuses: string[] = [];
  let count = 0;
  for (const task of tasks || []) {
    const status = task?.status || '';
    if (status in lookup) continue;
    count += 1;
    if (!statuses.includes(status)) statuses.push(status);
  }
  const rank = (status: string) => {
    const index = TASK_BOARD_STATUSES.indexOf(status);
    return index < 0 ? TASK_BOARD_STATUSES.length : index;
  };
  statuses.sort((a, b) => rank(a) - rank(b) || a.localeCompare(b));
  return { count, statuses };
}

// ─── Validation ───

/** Derive a column id from its label, the same way the server does. */
export function slugifyColumnLabel(label: string, index: number): string {
  let slug = '';
  let lastDash = true;
  for (const char of (label || '').toLowerCase()) {
    if ((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9')) {
      slug += char;
      lastDash = false;
    } else if (!lastDash) {
      slug += '-';
      lastDash = true;
    }
  }
  slug = slug.replace(/^-+|-+$/g, '');
  return slug || `column-${index + 1}`;
}

function columnId(column: TaskBoardColumn | undefined, index: number): string {
  const id = (column?.id || '').trim();
  return id || slugifyColumnLabel((column?.label || '').trim(), index);
}

/**
 * What is wrong with one column, phrased for display next to it. `''` when the
 * column is fine. Every case here is a 400 on the server.
 */
export function columnProblem(columns: TaskBoardColumn[] | undefined, index: number): string {
  const list = columns || [];
  const column = list[index];
  if (!column) return '';

  const label = (column.label || '').trim();
  if (!label) return 'This column needs a name.';
  if (label.length > TASK_BOARD_MAX_LABEL_LENGTH) {
    return `This name is ${label.length} characters. The maximum is ${TASK_BOARD_MAX_LABEL_LENGTH}.`;
  }

  const id = columnId(column, index);
  const twin = list.findIndex((other, i) => i !== index && columnId(other, i) === id);
  if (twin >= 0) {
    return `Column ${twin + 1} resolves to the same name. Give the two columns different names.`;
  }

  const statuses = column.statuses || [];
  if (!statuses.length) return 'Pick at least one status, or nothing can ever appear here.';

  for (const status of statuses) {
    const owner = statusOwnerIndex(list, status, index);
    if (owner >= 0) {
      const other = (list[owner]?.label || '').trim() || `column ${owner + 1}`;
      return `"${taskStatusText(status)}" is also in "${other}". A status can only be in one column.`;
    }
  }
  return '';
}

/**
 * The one thing blocking a save, or `''`. Board-level limits first, then the
 * first column that has a problem, named by position so it can be found.
 */
export function validateTaskBoardColumns(columns: TaskBoardColumn[] | undefined): string {
  const list = columns || [];
  if (!list.length) return 'A board needs at least one column.';
  if (list.length > TASK_BOARD_MAX_COLUMNS) {
    return `${list.length} columns is more than the maximum of ${TASK_BOARD_MAX_COLUMNS}.`;
  }
  for (let i = 0; i < list.length; i += 1) {
    const problem = columnProblem(list, i);
    if (problem) return `Column ${i + 1}: ${problem}`;
  }
  return '';
}

// ─── Ordering ───

/**
 * `list` with the item at `from` moved to `to`. Returns a new array, and the
 * original when the move would be a no-op or is out of range.
 */
export function moveItem<T>(list: T[] | undefined, from: number, to: number): T[] {
  const items = [...(list || [])];
  if (from === to || from < 0 || to < 0 || from >= items.length || to >= items.length) return items;
  const [moved] = items.splice(from, 1);
  items.splice(to, 0, moved);
  return items;
}
