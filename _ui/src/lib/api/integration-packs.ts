import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface RequiredVariable {
  key: string;
  description: string;
  secret: boolean;
}

export interface PackCounts {
  skills: number;
  mcp_sets: number;
  agents: number;
  organization: boolean;
}

export interface IntegrationPackSummary {
  slug: string;
  name: string;
  description: string;
  category: string;
  icon?: string;
  author?: string;
  version: string;
  read_only: boolean;
  source: string;
  source_url?: string;
  source_id?: string;
  counts: PackCounts;
  variables?: RequiredVariable[];
}

export interface IntegrationSkill {
  name: string;
  description: string;
  category: string;
  tags?: string[];
  system_prompt: string;
}

export interface IntegrationMCPSet {
  name: string;
  description: string;
  category: string;
  tags?: string[];
}

export interface IntegrationAgent {
  name: string;
  config: Record<string, any>;
}

export interface IntegrationOrganization {
  name: string;
  description: string;
  relationships?: Array<{
    agent_name: string;
    role?: string;
    is_head?: boolean;
  }>;
}

export interface IntegrationComponents {
  skills?: IntegrationSkill[];
  mcp_sets?: IntegrationMCPSet[];
  agents?: IntegrationAgent[];
  organization?: IntegrationOrganization;
}

export interface IntegrationPack extends IntegrationPackSummary {
  components: IntegrationComponents;
}

export interface PackInstallRequest {
  skills: boolean;
  mcp_sets: boolean;
  agents?: string[];
  organization: boolean;
}

export interface PackInstallResult {
  skills_created: number;
  mcp_sets_created: number;
  agents_created: number;
  organization_id?: string;
}

export async function listIntegrationPacks(): Promise<IntegrationPackSummary[]> {
  const res = await api.get<IntegrationPackSummary[]>('/integration-packs');
  return res.data;
}

export async function getIntegrationPack(slug: string): Promise<IntegrationPack> {
  const res = await api.get<IntegrationPack>(`/integration-packs/${slug}`);
  return res.data;
}

export async function installIntegrationPack(slug: string, req: PackInstallRequest): Promise<PackInstallResult> {
  const res = await api.post<PackInstallResult>(`/integration-packs/${slug}/install`, req);
  return res.data;
}

export async function createPack(data: { slug: string; name: string; description: string; category: string; version?: string }): Promise<any> {
  const res = await api.post('/integration-packs', data);
  return res.data;
}

export async function deletePack(slug: string): Promise<void> {
  await api.delete(`/integration-packs/${slug}`);
}

export async function addSkillToPack(slug: string, skill: { name: string; description: string; category: string; tags: string[]; system_prompt: string }): Promise<void> {
  await api.post(`/integration-packs/${slug}/skills`, skill);
}

export async function addAgentToPack(slug: string, agent: { name: string; config: Record<string, any> }): Promise<void> {
  await api.post(`/integration-packs/${slug}/agents`, agent);
}

export async function addMCPSetToPack(slug: string, mcpSet: Record<string, any>): Promise<void> {
  await api.post(`/integration-packs/${slug}/mcp-sets`, mcpSet);
}

export async function removeFromPack(slug: string, type: string, name: string): Promise<void> {
  await api.delete(`/integration-packs/${slug}/${type}/${name}`);
}
