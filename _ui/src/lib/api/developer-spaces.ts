import axios from 'axios';
import { authFetch, workspaceTransport } from './transport';

const api = axios.create({ baseURL: 'api/v1' });

export interface DeveloperSpaceConfig { disable_formatter?: boolean }

export const DEFAULT_DEVELOPER_IMAGE = 'debian:13.7-slim';

export interface DeveloperRuntime {
  backend: string; preserves_root_on_stop: boolean; persistent_home: boolean;
  multi_replica: boolean; notice?: string;
}

export interface DeveloperSpace {
  id: string; workspace_id: string; owner_user_id: string;
  status: 'pending' | 'ready' | 'stopped' | 'error'; image?: string;
  cpu_limit?: string; memory_limit?: string; disk_limit_bytes?: number;
  config: DeveloperSpaceConfig; error?: string; last_active_at?: string;
  created_at: string; updated_at: string;
  runtime?: DeveloperRuntime;
  execution_suspended?: boolean;
  active_control_id?: string;
}

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

// ─── Tools for Chats ───

export interface DeveloperToolDef {
  name: string; description: string; input_schema: Record<string, unknown>;
  /** Chats asks the reader before each call. */
  confirm: boolean;
  /** The tool changes files and reports a diff. */
  edits: boolean;
}

export interface DeveloperToolsInfo {
  tools: DeveloperToolDef[];
  space: { status: DeveloperSpace['status']; execution_suspended: boolean };
}

export interface DeveloperToolResult {
  output: string; error?: string; project?: string;
  changed?: string[]; diff?: string; diff_truncated?: boolean;
}

export async function listDeveloperTools(signal?: AbortSignal) { return (await api.get<DeveloperToolsInfo>('/developer-space/tools', { signal })).data; }

export async function callDeveloperTool(body: { name: string; arguments: Record<string, unknown>; project: string; trace_id?: string; session_id?: string }, signal?: AbortSignal) {
  return (await api.post<DeveloperToolResult>('/developer-space/tools/call', body, { signal })).data;
}

/** WebSockets cannot send X-AT-Workspace-ID, so the selection rides the query. */
export function developerTerminalURL(cwd: string) {
  const url = new URL(`api/v1/developer-space/terminal`, document.baseURI);
  if (cwd) url.searchParams.set('cwd', cwd);
  if (workspaceTransport.selected) url.searchParams.set('workspace_id', workspaceTransport.selected);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  return url.href;
}
