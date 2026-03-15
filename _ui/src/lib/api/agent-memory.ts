import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface AgentMemory {
  id: string;
  agent_id: string;
  organization_id: string;
  task_id: string;
  task_identifier: string;
  summary_l0: string;
  summary_l1: string;
  tags: string[];
  created_at: string;
}

export interface AgentMemoryMessages {
  memory_id: string;
  messages: any[];
}

export async function listOrgMemories(orgId: string, agentId?: string): Promise<AgentMemory[]> {
  const params: Record<string, string> = {};
  if (agentId) {
    params.agent_id = agentId;
  }
  const res = await api.get<AgentMemory[]>(`/organizations/${orgId}/memories`, { params });
  return res.data;
}

export async function searchOrgMemories(orgId: string, query: string, agentId?: string): Promise<AgentMemory[]> {
  const body: { query: string; agent_id?: string } = { query };
  if (agentId) {
    body.agent_id = agentId;
  }
  const res = await api.post<AgentMemory[]>(`/organizations/${orgId}/memories/search`, body);
  return res.data;
}

export async function getAgentMemory(id: string): Promise<AgentMemory> {
  const res = await api.get<AgentMemory>(`/agent-memories/${id}`);
  return res.data;
}

export async function getAgentMemoryMessages(id: string): Promise<AgentMemoryMessages> {
  const res = await api.get<AgentMemoryMessages>(`/agent-memories/${id}/messages`);
  return res.data;
}

export async function deleteAgentMemory(id: string): Promise<void> {
  await api.delete(`/agent-memories/${id}`);
}
