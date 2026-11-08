import axios from 'axios';

export { MCP_AUTH_PARAMS, mcpAuthParams, safeMCPAuthRedirect } from '../helper/mcp-auth';

const api = axios.create({
  baseURL: 'api/v1',
});

/** A pending authorization request, as the consent page shows it. */
export interface MCPAuthRequestInfo {
  valid: boolean;
  message?: string;
  client_name?: string;
  client_dynamic?: boolean;
  /** Host of a Client ID Metadata Document client: the verified app origin. */
  client_verified_host?: string;
  client_uri?: string;
  server_name?: string;
  server_id?: string;
  description?: string;
  workspace_id?: string;
  redirect_host?: string;
  resource?: string;
  /** The selected workspace does not own the server. */
  workspace_mismatch?: boolean;
  /** The signed-in account may use the server. */
  allowed?: boolean;
}

/** An OAuth upstream still to connect, chained after consent. */
export interface MCPAuthPendingAccount {
  set_id: string;
  set_name: string;
  upstream_index: number;
  provider: string;
  server: string;
}

export interface MCPAuthDecision {
  redirect: string;
  pending_accounts?: MCPAuthPendingAccount[];
  /** Set on a check: whether a remembered consent applied. */
  remembered?: boolean;
}

/** One of the signed-in account's MCP sign-ins. */
export interface MCPAuthGrant {
  id: string;
  mcp_server_id: string;
  server_name?: string;
  client_id: string;
  client_name: string;
  resource: string;
  created_at: string;
  last_used_at?: string;
  /** While set, new sign-ins through this client are approved without asking. */
  remembered_until?: string;
}

/** An installation-wide view of one registered client, for administrators. */
export interface MCPAuthClientUsage {
  id: string;
  client_id: string;
  name: string;
  redirect_uris: string[];
  dynamic: boolean;
  metadata_document?: boolean;
  client_uri?: string;
  confidential: boolean;
  workspace_id?: string;
  mcp_server_id?: string;
  server_name?: string;
  created_at: string;
  last_used_at?: string;
  grants: number;
  active_users: number;
  users: { user_id: string; username?: string; workspace_id: string; server_name?: string; created_at: string; last_used_at?: string }[];
}

// Both calls are answered in the workspace that owns the MCP server, which
// may differ from the tab's selection. The workspace transport replaces
// X-AT-Workspace-ID on scoped calls, so the override rides a query selector
// the server accepts only on these two routes; membership is still checked.
export async function getMCPAuthRequest(params: Record<string, string>, workspaceID: string): Promise<MCPAuthRequestInfo> {
  const res = await api.get<MCPAuthRequestInfo>('/mcp-auth/authorize', { params: { ...params, at_workspace: workspaceID } });
  return res.data;
}

export async function decideMCPAuthRequest(params: Record<string, string>, approve: boolean, workspaceID: string, remember = false): Promise<MCPAuthDecision> {
  const res = await api.post<MCPAuthDecision>(`/mcp-auth/authorize?at_workspace=${encodeURIComponent(workspaceID)}`, { params, approve, remember });
  return res.data;
}

/** Issues a code without asking when the account remembered this consent. */
export async function checkRememberedMCPAuth(params: Record<string, string>, workspaceID: string): Promise<MCPAuthDecision> {
  const res = await api.post<MCPAuthDecision>(`/mcp-auth/authorize?at_workspace=${encodeURIComponent(workspaceID)}`, { params, approve: true, check: true });
  return res.data;
}

export async function forgetMCPAuthConsent(id: string): Promise<void> {
  await api.delete(`/mcp-auth/grants/${encodeURIComponent(id)}/consent`);
}

export async function listAllMCPAuthClients(): Promise<MCPAuthClientUsage[]> {
  const res = await api.get<MCPAuthClientUsage[]>('/mcp-auth/clients');
  return res.data ?? [];
}

export async function adminDeleteMCPAuthClient(id: string): Promise<void> {
  await api.delete(`/mcp-auth/clients/${encodeURIComponent(id)}`);
}

export async function listMCPAuthGrants(): Promise<MCPAuthGrant[]> {
  const res = await api.get<MCPAuthGrant[]>('/mcp-auth/grants');
  return res.data ?? [];
}

export async function revokeMCPAuthGrant(id: string): Promise<void> {
  await api.delete(`/mcp-auth/grants/${encodeURIComponent(id)}`);
}
