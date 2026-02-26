import axios from 'axios';

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
  system_prompt: string;
  tools: SkillTool[];
  created_at: string;
  updated_at: string;
}

interface SkillsResponse {
  skills: Skill[];
}

// ─── API Functions ───

export async function listSkills(): Promise<Skill[]> {
  const res = await api.get<SkillsResponse>('/skills');
  return res.data.skills;
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
