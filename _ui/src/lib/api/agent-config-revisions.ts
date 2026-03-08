import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface AgentConfigRevision {
  id: string;
  agent_id: string;
  version: number;
  config_before: Record<string, any>;
  config_after: Record<string, any>;
  changed_by: string;
  change_note: string;
  created_at: string;
}

export async function listAgentConfigRevisions(agentId: string, params?: ListParams): Promise<ListResult<AgentConfigRevision>> {
  const res = await api.get<ListResult<AgentConfigRevision>>(`/agents/${agentId}/config-revisions`, { params });
  return res.data;
}

export async function getLatestAgentConfigRevision(agentId: string): Promise<AgentConfigRevision> {
  const res = await api.get<AgentConfigRevision>(`/agents/${agentId}/config-revisions/latest`);
  return res.data;
}

export async function getAgentConfigRevision(id: string): Promise<AgentConfigRevision> {
  const res = await api.get<AgentConfigRevision>(`/agent-config-revisions/${id}`);
  return res.data;
}
