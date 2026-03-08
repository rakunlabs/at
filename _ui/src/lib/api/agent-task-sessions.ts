import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface AgentTaskSession {
  id: string;
  agent_id: string;
  task_key: string;
  adapter_type: string;
  session_params_json: Record<string, any>;
  session_display_id: string;
  created_at: string;
  updated_at: string;
}

export async function listAgentTaskSessions(agentId: string, params?: ListParams): Promise<ListResult<AgentTaskSession>> {
  const res = await api.get<ListResult<AgentTaskSession>>(`/agents/${agentId}/task-sessions`, { params });
  return res.data;
}

export async function getAgentTaskSession(agentId: string, taskKey: string): Promise<AgentTaskSession> {
  const res = await api.get<AgentTaskSession>(`/agents/${agentId}/task-sessions/${taskKey}`);
  return res.data;
}

export async function upsertAgentTaskSession(agentId: string, taskKey: string, data: Partial<AgentTaskSession>): Promise<AgentTaskSession> {
  const res = await api.put<AgentTaskSession>(`/agents/${agentId}/task-sessions/${taskKey}`, data);
  return res.data;
}

export async function deleteAgentTaskSession(agentId: string, taskKey: string): Promise<void> {
  await api.delete(`/agents/${agentId}/task-sessions/${taskKey}`);
}
