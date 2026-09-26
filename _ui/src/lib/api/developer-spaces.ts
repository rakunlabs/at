import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface DeveloperToolRule { action: string; resource: string; effect: 'allow' | 'ask' | 'deny' }
export interface DeveloperAgentProfile { system_prompt?: string; max_iterations?: number; tool_timeout_seconds?: number; rules?: DeveloperToolRule[] }
export interface DeveloperSpaceConfig { plan?: DeveloperAgentProfile; build?: DeveloperAgentProfile; review?: DeveloperAgentProfile }

export interface DeveloperSpace {
  id: string; workspace_id: string; owner_user_id: string; name: string;
  status: 'pending' | 'ready' | 'stopped' | 'error'; image?: string;
  cpu_limit?: string; memory_limit?: string; disk_limit_bytes?: number;
  config: DeveloperSpaceConfig; error?: string; last_active_at?: string;
  created_at: string; updated_at: string;
}

export interface DeveloperRepository {
  id: string; space_id: string; name: string; remote_url: string; default_branch?: string;
  status: 'pending' | 'cloning' | 'ready' | 'error'; head_sha?: string; error?: string;
  created_at: string; updated_at: string;
}

export interface DeveloperWorktree {
  id: string; repository_id: string; space_id: string; name: string; branch: string;
  base_ref?: string; head_sha?: string; state: 'pending' | 'ready' | 'invalid' | 'missing';
  error?: string; created_at: string; updated_at: string;
}

export interface DeveloperSession {
  id: string; space_id: string; repository_id?: string; worktree_id?: string; title?: string;
  mode: 'plan' | 'build' | 'review'; status: string; provider?: string; model?: string;
  error?: string; created_at: string; updated_at: string;
}
export interface DeveloperSessionMessage { id: string; session_id: string; role: string; content: unknown; created_at: string }
export interface DeveloperToolCall { ID: string; Name: string; Arguments: Record<string, unknown> }
export interface DeveloperPendingTool { session_id: string; kind: 'permission' | 'question'; state: 'pending' | 'executing'; tool_calls: DeveloperToolCall[]; trace_id?: string; step: number; created_at: string }
export interface DeveloperSessionSnapshot { id: string; session_id: string; step: number; phase: 'before' | 'after'; head_sha?: string; storage_object_id?: string; created_at: string }
export interface DeveloperSnapshotPayload { version: number; head_sha: string; diff: string; untracked: string[]; truncated?: boolean }

export async function listDeveloperSpaces() { return (await api.get<DeveloperSpace[]>('/developer-spaces')).data; }
export async function createDeveloperSpace(body: Partial<DeveloperSpace>) { return (await api.post<DeveloperSpace>('/developer-spaces', body)).data; }
export async function deleteDeveloperSpace(id: string) { await api.delete(`/developer-spaces/${encodeURIComponent(id)}`); }
export async function startDeveloperSpace(id: string) { return (await api.post<DeveloperSpace>(`/developer-spaces/${encodeURIComponent(id)}/start`)).data; }
export async function stopDeveloperSpace(id: string) { return (await api.post<DeveloperSpace>(`/developer-spaces/${encodeURIComponent(id)}/stop`)).data; }

export async function listDeveloperRepositories(spaceID: string) { return (await api.get<DeveloperRepository[]>(`/developer-spaces/${encodeURIComponent(spaceID)}/repositories`)).data; }
export async function createDeveloperRepository(spaceID: string, body: Partial<DeveloperRepository>) { return (await api.post<DeveloperRepository>(`/developer-spaces/${encodeURIComponent(spaceID)}/repositories`, body)).data; }
export async function cloneDeveloperRepository(id: string) { return (await api.post<DeveloperRepository>(`/developer-repositories/${encodeURIComponent(id)}/clone`)).data; }
export async function deleteDeveloperRepository(id: string) { await api.delete(`/developer-repositories/${encodeURIComponent(id)}`); }

export async function listDeveloperWorktrees(repositoryID: string) { return (await api.get<DeveloperWorktree[]>(`/developer-repositories/${encodeURIComponent(repositoryID)}/worktrees`)).data; }
export async function createDeveloperWorktree(repositoryID: string, body: Partial<DeveloperWorktree>) { return (await api.post<DeveloperWorktree>(`/developer-repositories/${encodeURIComponent(repositoryID)}/worktrees`, body)).data; }
export async function provisionDeveloperWorktree(id: string) { return (await api.post<DeveloperWorktree>(`/developer-worktrees/${encodeURIComponent(id)}/provision`)).data; }
export async function getDeveloperWorktreeStatus(id: string) { return (await api.get<{worktree_id: string; porcelain_v2: string}>(`/developer-worktrees/${encodeURIComponent(id)}/status`)).data; }
export async function getDeveloperWorktreeDiff(id: string, staged = false) { return (await api.get<{worktree_id: string; diff: string}>(`/developer-worktrees/${encodeURIComponent(id)}/diff`, { params: { staged } })).data; }
export async function stageDeveloperWorktree(id: string, paths: string[]) { return (await api.post<{staged: string[]}>(`/developer-worktrees/${encodeURIComponent(id)}/stage`, { paths })).data; }
export async function commitDeveloperWorktree(id: string, message: string) { return (await api.post<DeveloperWorktree>(`/developer-worktrees/${encodeURIComponent(id)}/commit`, { message })).data; }
export async function pushDeveloperWorktree(id: string, branch: string) { return (await api.post<{branch: string; output: string}>(`/developer-worktrees/${encodeURIComponent(id)}/push`, { confirm: true, branch })).data; }
export async function deleteDeveloperWorktree(id: string) { await api.delete(`/developer-worktrees/${encodeURIComponent(id)}`); }

export async function listDeveloperSessions(spaceID: string) { return (await api.get<DeveloperSession[]>(`/developer-spaces/${encodeURIComponent(spaceID)}/sessions`)).data; }
export async function createDeveloperSession(spaceID: string, body: Partial<DeveloperSession>) { return (await api.post<DeveloperSession>(`/developer-spaces/${encodeURIComponent(spaceID)}/sessions`, body)).data; }
export async function deleteDeveloperSession(id: string) { await api.delete(`/developer-sessions/${encodeURIComponent(id)}`); }
export async function listDeveloperSessionMessages(id: string) { return (await api.get<DeveloperSessionMessage[]>(`/developer-sessions/${encodeURIComponent(id)}/messages`)).data; }
export async function listDeveloperSessionSnapshots(id: string) { return (await api.get<DeveloperSessionSnapshot[]>(`/developer-sessions/${encodeURIComponent(id)}/snapshots`)).data; }
export async function getDeveloperSessionSnapshot(sessionID: string, snapshotID: string) { return (await api.get<DeveloperSnapshotPayload>(`/developer-sessions/${encodeURIComponent(sessionID)}/snapshots/${encodeURIComponent(snapshotID)}`)).data; }
export async function getDeveloperSessionPendingTool(id: string) { return (await api.get<DeveloperPendingTool | null>(`/developer-sessions/${encodeURIComponent(id)}/pending`)).data; }
export async function runDeveloperSession(id: string, prompt: string) { return (await api.post<{session: DeveloperSession; content?: string; pending_tool?: DeveloperPendingTool}>(`/developer-sessions/${encodeURIComponent(id)}/run`, { prompt })).data; }
export async function confirmDeveloperSessionTool(id: string, approved: boolean) { return (await api.post<{session: DeveloperSession; content?: string; pending_tool?: DeveloperPendingTool}>(`/developer-sessions/${encodeURIComponent(id)}/confirm`, { approved })).data; }
export async function answerDeveloperSessionQuestion(id: string, answer: string) { return (await api.post<{session: DeveloperSession; content?: string; pending_tool?: DeveloperPendingTool}>(`/developer-sessions/${encodeURIComponent(id)}/answer`, { answer })).data; }
export async function cancelDeveloperSession(id: string) { return (await api.post<DeveloperSession>(`/developer-sessions/${encodeURIComponent(id)}/cancel`)).data; }
export function developerWorktreeTerminalURL(id: string) {
  const base = new URL(document.baseURI);
  const path = `${base.pathname.replace(/\/$/, '')}/api/v1/developer-worktrees/${encodeURIComponent(id)}/terminal`;
  return `${base.protocol === 'https:' ? 'wss:' : 'ws:'}//${base.host}${path}`;
}
