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
}

// Both calls are answered in the workspace that owns the MCP server, which
// may differ from the tab's selection. The workspace transport replaces
// X-AT-Workspace-ID on scoped calls, so the override rides a query selector
// the server accepts only on these two routes; membership is still checked.
export async function getMCPAuthRequest(params: Record<string, string>, workspaceID: string): Promise<MCPAuthRequestInfo> {
  const res = await api.get<MCPAuthRequestInfo>('/mcp-auth/authorize', { params: { ...params, at_workspace: workspaceID } });
  return res.data;
}

export async function decideMCPAuthRequest(params: Record<string, string>, approve: boolean, workspaceID: string): Promise<MCPAuthDecision> {
  const res = await api.post<MCPAuthDecision>(`/mcp-auth/authorize?at_workspace=${encodeURIComponent(workspaceID)}`, { params, approve });
  return res.data;
}

export async function listMCPAuthGrants(): Promise<MCPAuthGrant[]> {
  const res = await api.get<MCPAuthGrant[]>('/mcp-auth/grants');
  return res.data ?? [];
}

export async function revokeMCPAuthGrant(id: string): Promise<void> {
  await api.delete(`/mcp-auth/grants/${encodeURIComponent(id)}`);
}
