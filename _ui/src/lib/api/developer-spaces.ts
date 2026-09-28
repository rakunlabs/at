import axios from 'axios';
import { authFetch, workspaceTransport } from './transport';

const api = axios.create({ baseURL: 'api/v1' });

export interface DeveloperToolRule { action: string; resource: string; effect: 'allow' | 'ask' | 'deny' }
export interface DeveloperAgentProfile { system_prompt?: string; max_iterations?: number; tool_timeout_seconds?: number; rules?: DeveloperToolRule[] }
export interface DeveloperSpaceConfig { plan?: DeveloperAgentProfile; build?: DeveloperAgentProfile; review?: DeveloperAgentProfile }

export const DEFAULT_DEVELOPER_IMAGE = 'debian:13.7-slim';

export interface DeveloperSpace {
  id: string; workspace_id: string; owner_user_id: string;
  status: 'pending' | 'ready' | 'stopped' | 'error'; image?: string;
  cpu_limit?: string; memory_limit?: string; disk_limit_bytes?: number;
  config: DeveloperSpaceConfig; error?: string; last_active_at?: string;
  created_at: string; updated_at: string;
}

export type DeveloperMode = 'plan' | 'build' | 'review';
export type DeveloperSessionStatus = 'idle' | 'running' | 'waiting_permission' | 'waiting_question' | 'completed' | 'failed' | 'cancelled';

export interface DeveloperSession {
  id: string; space_id: string; project_path: string; title?: string;
  mode: DeveloperMode; status: DeveloperSessionStatus; provider?: string; model?: string;
  /** Runs as this workspace agent; empty uses the built-in profile named by `mode`. */
  agent_id?: string;
  error?: string; started_at?: string; finished_at?: string; created_at: string; updated_at: string;
}

export interface DeveloperContentBlock {
  type: string; text?: string; thinking?: string; id?: string; name?: string;
  input?: Record<string, unknown>; tool_use_id?: string; content?: unknown;
}
export interface DeveloperSessionMessage { id: string; session_id: string; role: 'user' | 'assistant' | 'tool' | 'system'; content: string | DeveloperContentBlock[]; created_at: string }
export interface DeveloperToolCall { ID: string; Name: string; Arguments: Record<string, unknown> }
export interface DeveloperPendingTool { session_id: string; kind: 'permission' | 'question'; state: 'pending' | 'executing'; tool_calls: DeveloperToolCall[]; trace_id?: string; step: number; created_at: string }

export interface DeveloperFileEntry { name: string; path: string; type: 'file' | 'dir' | 'symlink'; size: number; mtime: number }
export interface DeveloperFileContent { path: string; size: number; content?: string; version?: string; binary?: boolean; too_large?: boolean }
export interface DeveloperSearchMatch { path: string; line: number; text: string }

export interface DeveloperGitFile { path: string; orig_path?: string; index: string; worktree: string }
export interface DeveloperGitChange { path: string; additions: number; deletions: number; binary?: boolean }
export interface DeveloperGitStatus {
  repository: boolean; branch?: string; upstream?: string; ahead: number; behind: number;
  staged: DeveloperGitFile[]; unstaged: DeveloperGitFile[]; untracked: string[]; conflicted: string[];
  /** Present only when requested with numstat: line counts against HEAD. */
  changes?: DeveloperGitChange[];
}
export interface DeveloperGitBranch { name: string; current: boolean; remote: boolean }

// ─── Space ───

export async function getDeveloperSpace() { return (await api.get<DeveloperSpace>('/developer-space')).data; }
export async function updateDeveloperSpace(body: Partial<DeveloperSpace>) { return (await api.put<DeveloperSpace>('/developer-space', body)).data; }
export async function startDeveloperSpace() { return (await api.post<DeveloperSpace>('/developer-space/start')).data; }
export async function stopDeveloperSpace() { return (await api.post<DeveloperSpace>('/developer-space/stop')).data; }
export async function resetDeveloperSpace() { return (await api.post<{ deleted: string; cleanup_warning?: string }>('/developer-space/reset', { confirm: true })).data; }

// ─── Home (per account, shared by the account's spaces in every workspace) ───

export interface DeveloperHome { enabled: boolean; path: string; default_path: string }

export async function getDeveloperHome() { return (await api.get<DeveloperHome>('/developer-space/home')).data; }
export async function updateDeveloperHome(body: { enabled: boolean; path: string }) { return (await api.put<DeveloperHome>('/developer-space/home', body)).data; }
/** Writes one file into the home at `path` (relative to it), default mode 600. */
export async function uploadDeveloperHomeFile(file: File, path = '', mode = '') {
  const form = new FormData();
  form.append('file', file);
  if (path) form.append('path', path);
  if (mode) form.append('mode', mode);
  return (await api.post<{ path: string; size: number; mode: string }>('/developer-space/home/files', form)).data;
}
export async function resetDeveloperHome() { return (await api.post<{ deleted: boolean }>('/developer-space/home/reset', { confirm: true })).data; }

// ─── Files ───

export async function listDeveloperFiles(path: string) { return (await api.get<{ path: string; entries: DeveloperFileEntry[]; truncated: boolean }>('/developer-space/files', { params: { path } })).data; }
export async function readDeveloperFile(path: string) { return (await api.get<DeveloperFileContent>('/developer-space/files/content', { params: { path } })).data; }
export async function writeDeveloperFile(path: string, content: string, version = '') { return (await api.put<{ path: string; size: number; version: string }>('/developer-space/files/content', { path, content, version })).data; }
export async function createDeveloperFile(path: string, type: 'file' | 'dir') { return (await api.post<DeveloperFileEntry>('/developer-space/files', { path, type })).data; }
export async function renameDeveloperFile(from: string, to: string) { return (await api.post<DeveloperFileEntry>('/developer-space/files/rename', { from, to })).data; }
export async function deleteDeveloperFile(path: string) { await api.delete('/developer-space/files', { params: { path } }); }
export async function uploadDeveloperFile(folder: string, file: File) {
  const form = new FormData();
  form.append('path', folder);
  form.append('file', file);
  return (await api.post<{ path: string }>('/developer-space/files/upload', form)).data;
}
export async function searchDeveloperFiles(project: string, pattern: string) { return (await api.get<{ matches: DeveloperSearchMatch[]; truncated: boolean }>('/developer-space/search', { params: { path: project, pattern } })).data; }
export async function cloneDeveloperProject(remote: string, name = '', branch = '') { return (await api.post<{ path: string }>('/developer-space/clone', { remote, name, branch })).data; }

/** Raw bytes for images/downloads. Fetched (not linked) so the workspace header is sent. */
export async function fetchDeveloperFileBlob(path: string): Promise<Blob> {
  const response = await authFetch(new URL(`api/v1/developer-space/files/raw?path=${encodeURIComponent(path)}`, document.baseURI));
  if (!response.ok) throw new Error((await response.text()) || `download failed (${response.status})`);
  return response.blob();
}

// ─── Git ───

export async function getDeveloperGitStatus(project: string, numstat = false) { return (await api.get<DeveloperGitStatus>('/developer-space/git/status', { params: numstat ? { project, numstat: true } : { project } })).data; }
export async function getDeveloperGitDiff(project: string, file = '', opts: { staged?: boolean; untracked?: boolean; head?: boolean } = {}) {
  return (await api.get<{ diff: string }>('/developer-space/git/diff', { params: { project, file, staged: !!opts.staged, untracked: !!opts.untracked, ...(opts.head ? { head: true } : {}) } })).data;
}
export async function listDeveloperGitBranches(project: string) { return (await api.get<DeveloperGitBranch[]>('/developer-space/git/branches', { params: { project } })).data; }
export async function stageDeveloperGit(project: string, paths: string[]) { await api.post('/developer-space/git/stage', { project, paths }); }
export async function unstageDeveloperGit(project: string, paths: string[]) { await api.post('/developer-space/git/unstage', { project, paths }); }
export async function discardDeveloperGit(project: string, paths: string[]) { await api.post('/developer-space/git/discard', { project, paths, confirm: true }); }
export async function commitDeveloperGit(project: string, message: string) { return (await api.post<{ head: string }>('/developer-space/git/commit', { project, message })).data; }
export async function checkoutDeveloperGit(project: string, branch: string, create = false) { await api.post('/developer-space/git/checkout', { project, branch, create }); }
export async function pullDeveloperGit(project: string) { return (await api.post<{ output: string }>('/developer-space/git/pull', { project })).data; }
export async function pushDeveloperGit(project: string, branch: string) { return (await api.post<{ branch: string; output: string }>('/developer-space/git/push', { project, branch, confirm: true })).data; }

// ─── Sessions ───

export async function listDeveloperSessions() { return (await api.get<DeveloperSession[]>('/developer-sessions')).data; }
export async function createDeveloperSession(body: { project_path: string; mode: DeveloperMode; agent_id?: string; provider?: string; model?: string; title?: string }) { return (await api.post<DeveloperSession>('/developer-sessions', body)).data; }
export async function updateDeveloperSession(id: string, body: { title?: string; mode?: DeveloperMode; agent_id?: string; provider?: string; model?: string }) { return (await api.patch<DeveloperSession>(`/developer-sessions/${encodeURIComponent(id)}`, body)).data; }
export async function deleteDeveloperSession(id: string) { await api.delete(`/developer-sessions/${encodeURIComponent(id)}`); }
export async function listDeveloperSessionMessages(id: string) { return (await api.get<DeveloperSessionMessage[]>(`/developer-sessions/${encodeURIComponent(id)}/messages`)).data; }
export async function getDeveloperSessionPendingTool(id: string) { return (await api.get<DeveloperPendingTool | null>(`/developer-sessions/${encodeURIComponent(id)}/pending`)).data; }
export async function cancelDeveloperSession(id: string) { return (await api.post<DeveloperSession>(`/developer-sessions/${encodeURIComponent(id)}/cancel`)).data; }

export type DeveloperStreamEvent =
  | { type: 'status' | 'done'; session: DeveloperSession }
  | { type: 'turn_start' }
  | { type: 'delta'; content?: string; reasoning?: string }
  | { type: 'message'; message: DeveloperSessionMessage }
  | { type: 'tool_start'; tool_id: string; tool_name: string }
  | { type: 'tool_result'; tool_id: string; tool_name: string; error: boolean; changed?: string[]; changed_tree?: boolean }
  | { type: 'pending'; pending_tool: DeveloperPendingTool }
  | { type: 'error'; error: string };

/**
 * Reads the run/confirm/answer event stream. Resolves after the terminal
 * `done` event, rejects on `error` or when the connection ends without one:
 * a dropped stream must not look like a finished turn.
 */
export async function streamDeveloperSession(
  id: string,
  action: 'run' | 'confirm' | 'answer',
  body: Record<string, unknown>,
  onEvent: (event: DeveloperStreamEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const response = await authFetch(new URL(`api/v1/developer-sessions/${encodeURIComponent(id)}/${action}`, document.baseURI), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  });
  if (!response.ok) {
    let message = `request failed (${response.status})`;
    try { const data = await response.json(); message = data.message || data.error || message; } catch { /* keep status */ }
    throw new Error(message);
  }
  await consumeDeveloperEvents(response, onEvent);
}

export async function consumeDeveloperEvents(response: Response, onEvent: (event: DeveloperStreamEvent) => void): Promise<void> {
  const reader = response.body?.getReader();
  if (!reader) throw new Error('No response body');
  const decoder = new TextDecoder();
  let buffer = '';
  let data: string[] = [];
  const dispatch = () => {
    if (!data.length) return false;
    const event = JSON.parse(data.join('\n')) as DeveloperStreamEvent;
    data = [];
    if (event.type === 'error') throw new Error(event.error || 'The agent could not complete this turn');
    onEvent(event);
    return event.type === 'done';
  };
  try {
    while (true) {
      const { done, value } = await reader.read();
      buffer += done ? decoder.decode() : decoder.decode(value, { stream: true });
      let end: number;
      while ((end = buffer.indexOf('\n')) !== -1) {
        const line = buffer.slice(0, end).replace(/\r$/, '');
        buffer = buffer.slice(end + 1);
        if (line === '') { if (dispatch()) return; continue; }
        if (line.startsWith('data:')) data.push(line.slice(5).replace(/^ /, ''));
      }
      if (done) {
        if (buffer.startsWith('data:')) data.push(buffer.slice(5).replace(/^ /, ''));
        if (dispatch()) return;
        throw new Error('Connection ended before the agent finished. Reload the session to see what was saved.');
      }
    }
  } finally {
    await reader.cancel().catch(() => {});
    reader.releaseLock();
  }
}

/** WebSockets cannot send X-AT-Workspace-ID, so the selection rides the query. */
export function developerTerminalURL(cwd: string) {
  const url = new URL(`api/v1/developer-space/terminal`, document.baseURI);
  if (cwd) url.searchParams.set('cwd', cwd);
  if (workspaceTransport.selected) url.searchParams.set('workspace_id', workspaceTransport.selected);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  return url.href;
}
