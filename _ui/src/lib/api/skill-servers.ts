import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({
  baseURL: 'api/v1',
});

export type SkillServerMode = 'package' | 'tools' | 'both';

export interface SkillServer {
  id: string;
  name: string;
  description: string;
  public?: boolean;
  mode: SkillServerMode;
  skills: string[];
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

export async function listSkillServers(params?: ListParams): Promise<ListResult<SkillServer>> {
  const res = await api.get<ListResult<SkillServer>>('/skill-servers', { params });
  return res.data;
}

export async function getSkillServer(id: string): Promise<SkillServer> {
  const res = await api.get<SkillServer>(`/skill-servers/${id}`);
  return res.data;
}

export async function createSkillServer(data: Partial<SkillServer>): Promise<SkillServer> {
  const res = await api.post<SkillServer>('/skill-servers', data);
  return res.data;
}

export async function updateSkillServer(id: string, data: Partial<SkillServer>): Promise<SkillServer> {
  const res = await api.put<SkillServer>(`/skill-servers/${id}`, data);
  return res.data;
}

export async function deleteSkillServer(id: string): Promise<void> {
  await api.delete(`/skill-servers/${id}`);
}
