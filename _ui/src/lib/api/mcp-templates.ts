import axios from 'axios';
import type { MCPServerConfig } from './mcp-servers';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface MCPTemplateSetData {
  name: string;
  description: string;
  config: MCPServerConfig;
}

export interface MCPTemplate {
  slug: string;
  name: string;
  description: string;
  category: string;
  tags: string[];
  mcp_server: MCPTemplateSetData;
}

// ─── API ───

export async function listMCPTemplates(category?: string): Promise<MCPTemplate[]> {
  const params: any = {};
  if (category) params.category = category;
  const res = await api.get<MCPTemplate[]>('/mcp-templates', { params });
  return res.data;
}

export async function getMCPTemplate(slug: string): Promise<MCPTemplate> {
  const res = await api.get<MCPTemplate>(`/mcp-templates/${slug}`);
  return res.data;
}

export async function installMCPTemplate(slug: string): Promise<any> {
  const res = await api.post(`/mcp-templates/${slug}/install`);
  return res.data;
}
