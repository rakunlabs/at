import axios from 'axios';
import type { ListParams, ListResult } from './types';

const api = axios.create({
  baseURL: 'api/v1',
});

export interface Marketplace {
  id: string;
  name: string;
  description: string;
  skills: string[];
  mcp_servers: string[];
  direct_mcp_servers: MarketplaceMCPServer[];
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

export interface MarketplaceMCPServer {
  name: string;
  description?: string;
  type?: string;
  url?: string;
  headers?: Record<string, string>;
  command?: string;
  args?: string[];
  env?: Record<string, string>;
}

export async function listMarketplaces(params?: ListParams): Promise<ListResult<Marketplace>> {
  const res = await api.get<ListResult<Marketplace>>('/marketplaces', { params });
  return res.data;
}

export async function createMarketplace(data: Partial<Marketplace>): Promise<Marketplace> {
  const res = await api.post<Marketplace>('/marketplaces', data);
  return res.data;
}

export async function updateMarketplace(id: string, data: Partial<Marketplace>): Promise<Marketplace> {
  const res = await api.put<Marketplace>(`/marketplaces/${id}`, data);
  return res.data;
}

export async function deleteMarketplace(id: string): Promise<void> {
  await api.delete(`/marketplaces/${id}`);
}
