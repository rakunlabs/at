import axios from 'axios';
import type { MCPSet } from './mcp-sets';
import type { MCPUpstream } from './mcp-servers';
import type { Skill } from './skills';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Remote marketplaces (Claude Code plugin marketplaces) ───

export interface MarketplaceSource {
  id: string;
  name: string;
  /** url: a marketplace.json over https; git: a repository; generic: legacy, not browsable */
  type: 'url' | 'git' | 'generic' | string;
  url: string;
  ref?: string;
  path?: string;
  credential_id?: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface RemotePlugin {
  name: string;
  display_name?: string;
  description?: string;
  version?: string;
  category?: string;
  tags?: string[];
  source_type: string;
  installable: boolean;
  reason?: string;
}

export interface RemoteMarketplace {
  source: MarketplaceSource;
  name: string;
  description?: string;
  plugins: RemotePlugin[];
}

export interface RemotePluginSkill {
  name: string;
  description?: string;
  path: string;
  checksum: string;
  resources: number;
  installed_id?: string;
  update_available?: boolean;
}

export interface RemotePluginMCP {
  name: string;
  type: 'http' | 'stdio';
  upstream: MCPUpstream;
  variables?: string[];
  plugin_relative?: boolean;
}

export interface RemotePluginDetail {
  plugin: RemotePlugin;
  skills: RemotePluginSkill[];
  mcp_servers: RemotePluginMCP[];
  warnings?: string[];
}

export interface MarketplaceInstallRequest {
  plugin: string;
  skills?: string[];
  skill_scope?: 'personal' | 'workspace';
  mcp_servers?: string[];
  mcp_set_id?: string;
  mcp_set_name?: string;
  mcp_scope?: 'personal' | 'workspace';
  /** ${VAR} → "var:<key>" (an AT variable) or a literal value */
  variables?: Record<string, string>;
  allow_stdio?: boolean;
}

export interface MarketplaceInstallResult {
  skills: Skill[];
  mcp_set?: MCPSet;
  warnings?: string[];
}

export interface MarketplaceMCPUpdate {
  index: number;
  plugin: string;
  server: string;
  update_available: boolean;
  error?: string;
}

export async function listMarketplaceSources(): Promise<MarketplaceSource[]> {
  const res = await api.get<MarketplaceSource[]>('/marketplace/sources');
  return res.data;
}

export async function createMarketplaceSource(src: Partial<MarketplaceSource>): Promise<MarketplaceSource> {
  const res = await api.post<MarketplaceSource>('/marketplace/sources', src);
  return res.data;
}

export async function updateMarketplaceSource(id: string, src: Partial<MarketplaceSource>): Promise<MarketplaceSource> {
  const res = await api.put<MarketplaceSource>(`/marketplace/sources/${id}`, src);
  return res.data;
}

export async function deleteMarketplaceSource(id: string): Promise<void> {
  await api.delete(`/marketplace/sources/${id}`);
}

export async function getMarketplaceCatalog(id: string): Promise<RemoteMarketplace> {
  const res = await api.get<RemoteMarketplace>(`/marketplace/sources/${id}/catalog`);
  return res.data;
}

export async function getMarketplacePlugin(id: string, plugin: string): Promise<RemotePluginDetail> {
  const res = await api.get<RemotePluginDetail>(`/marketplace/sources/${id}/plugins/${encodeURIComponent(plugin)}`);
  return res.data;
}

export async function installFromMarketplace(id: string, req: MarketplaceInstallRequest): Promise<MarketplaceInstallResult> {
  const res = await api.post<MarketplaceInstallResult>(`/marketplace/sources/${id}/install`, req);
  return res.data;
}

export async function checkMarketplaceMCPUpdates(setID: string): Promise<MarketplaceMCPUpdate[]> {
  const res = await api.get<{ updates: MarketplaceMCPUpdate[] }>('/marketplace/mcp-updates', { params: { set_id: setID } });
  return res.data.updates || [];
}

export async function applyMarketplaceMCPUpdates(setID: string, allowStdio = false): Promise<MCPSet> {
  const res = await api.post<MCPSet>('/marketplace/mcp-updates', { set_id: setID, allow_stdio: allowStdio });
  return res.data;
}
