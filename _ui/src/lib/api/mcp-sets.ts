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
