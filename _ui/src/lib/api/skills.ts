import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface SkillTool {
  name: string;
  description: string;
  inputSchema?: Record<string, any>;
  handler?: string;
  handler_type?: string; // "js" (default) or "bash"
}

export interface Skill {
  id: string;
  name: string;
  description: string;
  category?: string;
  tags?: string[];
  system_prompt: string;
  tools: SkillTool[];
  created_at: string;
  updated_at: string;
}

// ─── API Functions ───

export async function listSkills(params?: ListParams): Promise<ListResult<Skill>> {
  const res = await api.get<ListResult<Skill>>('/skills', { params });
  return res.data;
}

export async function getSkill(id: string): Promise<Skill> {
  const res = await api.get<Skill>(`/skills/${id}`);
  return res.data;
}

export async function createSkill(skill: Partial<Skill>): Promise<Skill> {
  const res = await api.post<Skill>('/skills', skill);
  return res.data;
}

export async function updateSkill(id: string, skill: Partial<Skill>): Promise<Skill> {
  const res = await api.put<Skill>(`/skills/${id}`, skill);
  return res.data;
}

export async function deleteSkill(id: string): Promise<void> {
  await api.delete(`/skills/${id}`);
}

// ─── Skill Templates ───

export interface RequiredVariable {
  key: string;
  description: string;
  secret: boolean;
}

export interface SkillTemplate {
  slug: string;
  name: string;
  description: string;
  category: string;
  tags: string[];
  required_variables: RequiredVariable[];
  oauth?: string; // e.g. "google" — needs OAuth connect flow
  skill: Omit<Skill, 'id' | 'created_at' | 'updated_at'>;
}

// ─── OAuth ───

export async function getOAuthStartURL(provider: string): Promise<string> {
  const res = await api.get<{ url: string }>('/oauth/start', { params: { provider } });
  return res.data.url;
}

export async function listSkillTemplates(category?: string): Promise<SkillTemplate[]> {
  const params: any = {};
  if (category) params.category = category;
  const res = await api.get<SkillTemplate[]>('/skill-templates', { params });
  return res.data;
}

export async function installSkillTemplate(slug: string): Promise<Skill> {
  const res = await api.post<Skill>(`/skill-templates/${slug}/install`);
  return res.data;
}

// ─── Import / Export ───

export async function importSkill(skill: Partial<Skill>): Promise<Skill> {
  const res = await api.post<Skill>('/skills/import', skill);
  return res.data;
}

export async function exportSkill(id: string): Promise<Partial<Skill>> {
  const res = await api.get<Partial<Skill>>(`/skills/${id}/export`);
  return res.data;
}

export async function exportSkillMD(id: string): Promise<string> {
  const res = await api.get<string>(`/skills/${id}/export-md`, { responseType: 'text' as any });
  return res.data;
}

export async function importSkillFromURL(url: string): Promise<Skill> {
  const res = await api.post<Skill>('/skills/import-url', { url });
  return res.data;
}

export async function importSkillMD(content: string): Promise<Skill> {
  const res = await api.post<Skill>('/skills/import-skillmd', { content });
  return res.data;
}

export async function previewImportURL(url: string): Promise<Partial<Skill>> {
  const res = await api.post<Partial<Skill>>('/skills/import-url/preview', { url });
  return res.data;
}

// ─── Test Handler ───

export interface TestHandlerRequest {
  handler: string;
  handler_type: string;
  arguments: Record<string, any>;
}

export interface TestHandlerResponse {
  result: string;
  error: string;
  duration_ms: number;
}

export async function testHandler(req: TestHandlerRequest): Promise<TestHandlerResponse> {
  const res = await api.post<TestHandlerResponse>('/skills/test-handler', req);
  return res.data;
}
