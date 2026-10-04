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
export interface BindingDetails { binding: ExecutionBinding | null; binding_valid: boolean; candidates: BindingCandidate[] }

export type BindingKind = 'bot' | 'mcp' | 'trigger';
const bindingCollections: Record<BindingKind, string> = { bot: 'bots', mcp: 'mcp/servers', trigger: 'triggers' };
const path = (kind: BindingKind, id: string) => `${bindingCollections[kind]}/${encodeURIComponent(id)}/execution-binding`;
export const getExecutionBinding = async (kind: BindingKind, id: string) =>
  (await api.get<BindingDetails>(path(kind, id))).data;
export const saveExecutionBinding = async (kind: BindingKind, id: string, user: string) =>
  (await api.post<ExecutionBinding>(path(kind, id), { run_as_user_id: user })).data;
export const revokeExecutionBinding = async (kind: BindingKind, id: string) =>
  (await api.delete<ExecutionBinding>(path(kind, id))).data;
