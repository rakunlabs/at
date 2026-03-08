import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface Organization {
  id: string;
  name: string;
  description: string;
  canvas_layout?: CanvasLayout;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

export interface CanvasLayout {
  groups?: CanvasGroup[];
  sticky_notes?: CanvasStickyNote[];
  agent_positions?: Record<string, { x: number; y: number }>;
}

export interface CanvasGroup {
  id: string;
  position: { x: number; y: number };
  width: number;
  height: number;
  label: string;
  color: string;
}

export interface CanvasStickyNote {
  id: string;
  position: { x: number; y: number };
  width: number;
  height: number;
  text: string;
  color: string;
}

export async function listOrganizations(params?: ListParams): Promise<ListResult<Organization>> {
  const res = await api.get<ListResult<Organization>>('/organizations', { params });
  return res.data;
}

export async function getOrganization(id: string): Promise<Organization> {
  const res = await api.get<Organization>(`/organizations/${id}`);
  return res.data;
}

export async function createOrganization(data: Partial<Organization>): Promise<Organization> {
  const res = await api.post<Organization>('/organizations', data);
  return res.data;
}

export async function updateOrganization(id: string, data: Partial<Organization>): Promise<Organization> {
  const res = await api.put<Organization>(`/organizations/${id}`, data);
  return res.data;
}

export async function deleteOrganization(id: string): Promise<void> {
  await api.delete(`/organizations/${id}`);
}

// ─── Organization–Agent Membership ───

export interface OrganizationAgent {
  id: string;
  organization_id: string;
  agent_id: string;
  role?: string;
  title?: string;
  parent_agent_id?: string;
  status?: string;
  created_at: string;
  updated_at: string;
}

export async function listOrgAgents(orgId: string): Promise<OrganizationAgent[]> {
  const res = await api.get<OrganizationAgent[]>(`/organizations/${orgId}/agents`);
  return res.data;
}

export async function addAgentToOrg(
  orgId: string,
  data: { agent_id: string; role?: string; title?: string; parent_agent_id?: string; status?: string },
): Promise<OrganizationAgent> {
  const res = await api.post<OrganizationAgent>(`/organizations/${orgId}/agents`, data);
  return res.data;
}

export async function updateOrgAgent(
  orgId: string,
  agentId: string,
  data: { role?: string; title?: string; parent_agent_id?: string; status?: string },
): Promise<OrganizationAgent> {
  const res = await api.put<OrganizationAgent>(`/organizations/${orgId}/agents/${agentId}`, data);
  return res.data;
}

export async function removeAgentFromOrg(orgId: string, agentId: string): Promise<void> {
  await api.delete(`/organizations/${orgId}/agents/${agentId}`);
}
