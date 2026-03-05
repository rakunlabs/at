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

export interface MCPServerConfig {
  description: string;
  // RAG integration
  enabled_rag_tools: string[];
  collection_ids: string[];
  fetch_mode: string;
  git_cache_dir: string;
  default_num_results: number;
  token_variable: string;
  token_user: string;
  ssh_key_variable: string;
  // HTTP tools
  http_tools: MCPHTTPTool[];
  // Upstream MCP servers
  mcp_upstreams?: MCPUpstream[];
  // Skill tools
  enabled_skills?: string[];
}

export interface MCPUpstream {
  url: string;
  headers?: Record<string, string>;
}

export interface MCPServer {
  id: string;
  name: string;
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
