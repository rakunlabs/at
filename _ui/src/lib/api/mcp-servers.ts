import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface MCPHTTPTool {
  name: string;
  description: string;
  method: string;
  url: string;
  headers?: Record<string, string>;
  body_template?: string;
  input_schema: Record<string, any>;
}

export interface MCPInlineTool {
  name: string;
  description: string;
  inputSchema: Record<string, any>;
  handler: string;
  handler_type?: string;
  source_skill_id?: string;
}

export interface MCPServerConfig {
  description?: string;
  // HTTP tools
  http_tools?: MCPHTTPTool[];
  // Legacy executable handlers migrated from documentation-only skills.
  // The MCP editor displays these as read-only and must preserve them on save.
  inline_tools?: MCPInlineTool[];
  // Upstream MCP servers
  mcp_upstreams?: MCPUpstream[];
  // Builtin tools
  enabled_builtin_tools?: string[];
  // Workflow tools
  workflow_ids?: string[];
  // Pins the provider/model (and defaults) generate_image uses on this endpoint
  image_generation?: ImageGenerationConfig;
  // MCP clients sign in with an AT account instead of an API token
  oauth?: MCPServerOAuth;
  // Raw WebSocket passthrough — exposes GET /gateway/v1/mcp/{name}/ws
  ws_upstream?: WSUpstream;
}

export interface ImageGenerationConfig {
  provider?: string;
  model?: string;
  size?: string;
  quality?: string;
  background?: string;
}

export interface WSUpstream {
  url: string; // ws:// or wss://
  headers?: Record<string, string>; // values support {{var:key}}
  pass_query_params?: string[]; // raw client query params to forward (empty = all except token)
  pass_headers?: string[]; // raw client headers to forward (Authorization/Cookie are blocked)
}

export type MCPAccountSource = 'user' | 'agent' | 'shared';

/** AT as the MCP authorization server for one gateway MCP server. */
export interface MCPServerOAuth {
  enabled: boolean;
  dynamic_clients: boolean;
  redirect_patterns?: string[];
  chain_upstreams: boolean;
}

/** A client pre-registered by an administrator for one MCP server. */
export interface MCPAuthClient {
  id: string;
  client_id: string;
  name: string;
  redirect_uris: string[];
  confidential: boolean;
  /** Present only in the response that created the client. */
  client_secret?: string;
  created_by?: string;
  created_at: string;
  last_used_at?: string;
}

export async function listMCPAuthClients(serverID: string): Promise<MCPAuthClient[]> {
  const res = await api.get<MCPAuthClient[]>(`/mcp/servers/${encodeURIComponent(serverID)}/oauth-clients`);
  return res.data ?? [];
}

export async function createMCPAuthClient(serverID: string, input: { name: string; redirect_uris: string[]; confidential: boolean }): Promise<MCPAuthClient> {
  const res = await api.post<MCPAuthClient>(`/mcp/servers/${encodeURIComponent(serverID)}/oauth-clients`, input);
  return res.data;
}

export async function deleteMCPAuthClient(serverID: string, id: string): Promise<void> {
  await api.delete(`/mcp/servers/${encodeURIComponent(serverID)}/oauth-clients/${encodeURIComponent(id)}`);
}

// OAuth 2.1 for an HTTP upstream (MCP authorization spec). Account sources
// are tried in order; one that is not listed is never used.
export interface MCPUpstreamAuth {
  type: 'oauth2';
  provider?: string;
  accounts?: MCPAccountSource[];
  shared_connection_id?: string;
  scopes?: string[];
  client_id?: string;
  authorization_server?: string;
}

export interface MCPUpstream {
  url?: string;
  proxy?: string;
  insecure_skip_verify?: boolean;
  headers?: Record<string, string>;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
  auth?: MCPUpstreamAuth;
}

export interface MCPServer {
  id: string;
  name: string;
  description?: string;
  public?: boolean;
  servers?: string[];
  urls?: string[];
  config: MCPServerConfig;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

// ─── CRUD ───

export async function listMCPServers(params?: ListParams): Promise<ListResult<MCPServer>> {
  const res = await api.get<ListResult<MCPServer>>('/mcp/servers', { params });
  return res.data;
}

export async function getMCPServer(id: string): Promise<MCPServer> {
  const res = await api.get<MCPServer>(`/mcp/servers/${id}`);
  return res.data;
}

export async function createMCPServer(data: Partial<MCPServer>): Promise<MCPServer> {
  const res = await api.post<MCPServer>('/mcp/servers', data);
  return res.data;
}

export async function updateMCPServer(id: string, data: Partial<MCPServer>): Promise<MCPServer> {
  const res = await api.put<MCPServer>(`/mcp/servers/${id}`, data);
  return res.data;
}

export async function deleteMCPServer(id: string): Promise<void> {
  await api.delete(`/mcp/servers/${id}`);
}

// ─── Stdio upstream lifecycle ───

// Per-upstream status of the local (stdio) MCP subprocesses behind a record.
// index refers to the position in config.mcp_upstreams; HTTP upstreams are
// omitted (they have no local process).
export interface MCPStdioUpstreamStatus {
  index: number;
  command: string;
  args?: string[];
  running: boolean;
  pid?: number;
  started_at?: string;
  uptime_seconds?: number;
  exit_error?: string;
  error?: string; // restart failure
}

export interface MCPStdioStatusResult {
  name: string;
  upstreams: MCPStdioUpstreamStatus[];
}

export async function getMCPServerStdioStatus(id: string): Promise<MCPStdioStatusResult> {
  const res = await api.get<MCPStdioStatusResult>(`/mcp/servers/${id}/stdio-status`);
  return res.data;
}

export async function restartMCPServerStdio(id: string, index?: number): Promise<MCPStdioStatusResult> {
  const res = await api.post<MCPStdioStatusResult>(`/mcp/servers/${id}/stdio-restart`, index !== undefined ? { index } : {});
  return res.data;
}

export async function stopMCPServerStdio(id: string, index?: number): Promise<MCPStdioStatusResult> {
  const res = await api.post<MCPStdioStatusResult>(`/mcp/servers/${id}/stdio-stop`, index !== undefined ? { index } : {});
  return res.data;
}

// ─── Import / Export ───

export async function exportMCPServer(id: string): Promise<Partial<MCPServer>> {
  const res = await api.get<Partial<MCPServer>>(`/mcp/servers/${id}/export`);
  return res.data;
}

export async function importMCPServer(data: Partial<MCPServer>): Promise<MCPServer> {
  const res = await api.post<MCPServer>('/mcp/servers/import', data);
  return res.data;
}

export async function previewImportMCPServer(data: Partial<MCPServer>): Promise<Partial<MCPServer>> {
  const res = await api.post<Partial<MCPServer>>('/mcp/servers/import/preview', data);
  return res.data;
}
