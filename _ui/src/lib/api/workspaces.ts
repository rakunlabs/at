import axios from 'axios';
import { identityAPI } from './identity';
export const workspaceAPI = axios.create({ baseURL: 'api/v1' });
export interface Workspace { id: string; name: string; role?: string; archived: boolean; execution_enabled: boolean; created_at: string }
export interface Member { workspace_id: string; user_id: string; role: string; status: string; version: number }
export interface Invitation { id: string; role: string; user_id?: string; email?: string; expires_at: string; consumed: boolean }
export interface CreateInvitation { role: string; user_id?: string; email?: string; expires_at: string }
export interface IssuedInvitation { invitation: Invitation; token: string }
export interface Bundle { id: string; key: string; name: string; description: string; keys: string[]; key_patterns: Record<string, string[]>; resource_ids?: Record<string, string[]> }
export interface GrantSource { capability: string; resource_ids?: string[]; path_patterns?: string[]; source: string; permission_id?: string; provider_id?: string; mapping_id?: string }
export interface EffectiveAccess { workspace_id: string; user_id: string; role: string; membership_version: number; capabilities: string[]; patterns: Record<string, string[] | null>; sources: GrantSource[]; denied: string[]; execution_enabled: boolean }
export interface Mapping { id: string; workspace_id: string; provider_id: string; claim_kind: string; claim_value: string; permission_id: string }
export const listWorkspaces = async () => (await identityAPI.get<{ items: Workspace[] }>('workspaces')).data.items || [];
export const getCapabilities = async (id: string) => (await identityAPI.get<EffectiveAccess>(`workspaces/${encodeURIComponent(id)}/capabilities`)).data;
export const acceptInvitation = async (token: string) => (await identityAPI.post<Member>('invitations/accept', { token })).data;
export const createInvitation = async (workspace: string, body: CreateInvitation) => (await workspaceAPI.post<IssuedInvitation>(`workspaces/${encodeURIComponent(workspace)}/invitations`, body)).data;
