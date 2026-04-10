import axios from 'axios';
import type { ListResult, ListParams } from './types';
import type { MCPServerConfig } from './mcp-servers';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface MCPSet {
  id: string;
  name: string;
  description: string;
  category?: string;
  tags?: string[];
  config: MCPServerConfig;
  servers: string[];
  urls: string[];
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

// ─── CRUD ───

export async function listMCPSets(params?: ListParams): Promise<ListResult<MCPSet>> {
  const res = await api.get<ListResult<MCPSet>>('/mcp/sets', { params });
  return res.data;
}

export async function getMCPSet(id: string): Promise<MCPSet> {
  const res = await api.get<MCPSet>(`/mcp/sets/${id}`);
  return res.data;
}

export async function createMCPSet(data: Partial<MCPSet>): Promise<MCPSet> {
  const res = await api.post<MCPSet>('/mcp/sets', data);
  return res.data;
}

export async function updateMCPSet(id: string, data: Partial<MCPSet>): Promise<MCPSet> {
  const res = await api.put<MCPSet>(`/mcp/sets/${id}`, data);
  return res.data;
}

export async function deleteMCPSet(id: string): Promise<void> {
  await api.delete(`/mcp/sets/${id}`);
}

// ─── Import / Export ───

export async function exportMCPSet(id: string): Promise<Partial<MCPSet>> {
  const res = await api.get<Partial<MCPSet>>(`/mcp/sets/${id}/export`);
  return res.data;
}

export async function importMCPSet(data: Partial<MCPSet>): Promise<MCPSet> {
  const res = await api.post<MCPSet>('/mcp/sets/import', data);
  return res.data;
}

export async function previewImportMCPSet(data: Partial<MCPSet>): Promise<Partial<MCPSet>> {
  const res = await api.post<Partial<MCPSet>>('/mcp/sets/import/preview', data);
  return res.data;
}

// ─── Tool Resolution (for Chat UI) ───

export interface MCPSetTool {
  name: string;
  description: string;
  inputSchema: Record<string, any>;
}

export interface MCPSetToolsResult {
  tools: MCPSetTool[];
}

export interface MCPSetToolCallResult {
  content: Array<{ type: string; text: string }>;
}

export async function listMCPSetTools(name: string): Promise<MCPSetToolsResult> {
  const res = await api.get<MCPSetToolsResult>(`/mcp/set-tools/${name}`);
  return res.data;
}

export async function callMCPSetTool(
  name: string,
  toolName: string,
  args: Record<string, any>,
): Promise<MCPSetToolCallResult> {
  const res = await api.post<MCPSetToolCallResult>(`/mcp/set-tools/${name}/call`, {
    tool_name: toolName,
    arguments: args,
  });
  return res.data;
}
