import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface ExecutionBinding {
  id: string;
  user_id: string;
  workspace_id: string;
  version: number;
  policy_version: number;
  revoked: boolean;
}

export interface BindingCandidate { user_id: string; name: string; role: string }
export interface BindingDetails { binding: ExecutionBinding | null; candidates: BindingCandidate[] }

const path = (kind: 'bot' | 'mcp', id: string) => `${kind === 'bot' ? 'bots' : 'mcp/servers'}/${encodeURIComponent(id)}/execution-binding`;
export const getExecutionBinding = async (kind: 'bot' | 'mcp', id: string) =>
  (await api.get<BindingDetails>(path(kind, id))).data;
export const saveExecutionBinding = async (kind: 'bot' | 'mcp', id: string, user: string) =>
  (await api.post<ExecutionBinding>(path(kind, id), { run_as_user_id: user })).data;
export const revokeExecutionBinding = async (kind: 'bot' | 'mcp', id: string) =>
  (await api.delete<ExecutionBinding>(path(kind, id))).data;
