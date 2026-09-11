import axios from 'axios';
import type { TaskBoard, TaskBoardColumn } from '../helper/task-board';

// `tests/task-board.test.mjs` compiles this file alone to a data URL, so the
// only import that may survive compilation is `axios`. The types above are
// erased; every value used here is declared locally.

const api = axios.create({ baseURL: 'api/v1' });

export type { TaskBoard, TaskBoardColumn };

// ─── Request body ───

/**
 * Exactly what `PUT /task-board` accepts. The endpoint decodes with
 * `DisallowUnknownFields`, so an extra key is a 400 rather than an ignored
 * field: the body is rebuilt from an allowlist instead of forwarded.
 */
export interface TaskBoardColumnBody {
  id?: string;
  label: string;
  color?: string;
  statuses: string[];
}

export interface TaskBoardBody {
  version: number;
  columns: TaskBoardColumnBody[];
}

const text = (value: unknown): string => (typeof value === 'string' ? value.trim() : '');

/**
 * `id` and `color` are omitted when blank rather than sent empty: an omitted
 * `id` is the documented signal for "derive it from the label", and a new
 * column has no id yet.
 */
export function taskBoardBody(version: number, columns: TaskBoardColumn[]): TaskBoardBody {
  return {
    version: typeof version === 'number' && Number.isFinite(version) ? version : 0,
    columns: (columns || []).map(column => {
      const body: TaskBoardColumnBody = {
        label: text(column?.label),
        statuses: (column?.statuses || []).map(text).filter(Boolean),
      };
      const id = text(column?.id);
      if (id) body.id = id;
      const color = text(column?.color);
      if (color) body.color = color;
      return body;
    }),
  };
}

// ─── Endpoints ───

/** The workspace layout, or the shipped default with `default: true`. */
export async function getTaskBoard(): Promise<TaskBoard> {
  const res = await api.get<TaskBoard>('/task-board');
  return res.data;
}

/**
 * Replace the layout. `version` is the one that was read; a mismatch is a 409,
 * meaning someone else saved first.
 */
export async function saveTaskBoard(version: number, columns: TaskBoardColumn[]): Promise<TaskBoard> {
  const res = await api.put<TaskBoard>('/task-board', taskBoardBody(version, columns));
  return res.data;
}

/** Drop the stored layout. Returns the default, so no second read is needed. */
export async function resetTaskBoard(): Promise<TaskBoard> {
  const res = await api.delete<TaskBoard>('/task-board');
  return res.data;
}

// ─── Errors ───

export function taskBoardErrorStatus(error: unknown): number {
  const status = (error as any)?.response?.status;
  return typeof status === 'number' ? status : 0;
}

/** Backend errors are `{"message":"…"}`; anything else falls back. */
export function taskBoardServerMessage(error: unknown, fallback: string): string {
  const message = (error as any)?.response?.data?.message;
  return typeof message === 'string' && message ? message : fallback;
}

/** A 409 means the stored board moved on; the local version is stale. */
export const isTaskBoardConflict = (error: unknown) => taskBoardErrorStatus(error) === 409;

/** A 403 means the reader may see the board but not change it. */
export const isTaskBoardForbidden = (error: unknown) => {
  const status = taskBoardErrorStatus(error);
  return status === 401 || status === 403;
};

/**
 * Save/reset failure text. A 400 names the offending column, so the server
 * message wins there; a 403 is about permission, which the server phrases
 * generically and the user needs spelled out.
 */
export function taskBoardErrorMessage(error: unknown, fallback = 'Could not save the board columns.'): string {
  switch (taskBoardErrorStatus(error)) {
    case 400:
      return taskBoardServerMessage(error, 'One of the columns is not valid. Check its name and statuses.');
    case 401:
    case 403:
      return 'You do not have permission to change the board columns. This needs the tasks.write capability.';
    case 409:
      return taskBoardServerMessage(error, 'The board was changed by someone else. Reload it and reapply your edit.');
    case 503:
      return taskBoardServerMessage(error, 'Board storage is unavailable right now. Retry when the server is ready.');
    default:
      return taskBoardServerMessage(error, fallback);
  }
}
