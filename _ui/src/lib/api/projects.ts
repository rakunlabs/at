import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface Project {
  id: string;
  organization_id: string;
  goal_id: string;
  lead_agent_id: string;
  name: string;
  description: string;
  status: string;
  color: string;
  target_date: string;
  archived_at: string;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

export async function listProjects(params?: ListParams): Promise<ListResult<Project>> {
  const res = await api.get<ListResult<Project>>('/projects', { params });
  return res.data;
}

export async function getProject(id: string): Promise<Project> {
  const res = await api.get<Project>(`/projects/${id}`);
  return res.data;
}

export async function createProject(data: Partial<Project>): Promise<Project> {
  const res = await api.post<Project>('/projects', data);
  return res.data;
}

export async function updateProject(id: string, data: Partial<Project>): Promise<Project> {
  const res = await api.put<Project>(`/projects/${id}`, data);
  return res.data;
}

export async function deleteProject(id: string): Promise<void> {
  await api.delete(`/projects/${id}`);
}

export async function listProjectsByGoal(goalId: string): Promise<Project[]> {
  const res = await api.get<Project[]>(`/goals/${goalId}/projects`);
  return res.data;
}

export async function listProjectsByOrganization(orgId: string): Promise<Project[]> {
  const res = await api.get<Project[]>(`/organizations/${orgId}/projects`);
  return res.data;
}
