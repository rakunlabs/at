import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface AgentRuntimeState {
  agent_id: string;
  session_id: string;
  state_json: Record<string, any>;
  total_input_tokens: number;
  total_output_tokens: number;
  total_cost_cents: number;
  last_run_id: string;
  last_run_status: string;
  last_error: string;
  updated_at: string;
}

export async function getAgentRuntimeState(agentId: string): Promise<AgentRuntimeState> {
  const res = await api.get<AgentRuntimeState>(`/agents/${agentId}/runtime-state`);
  return res.data;
}

export async function upsertAgentRuntimeState(agentId: string, data: Partial<AgentRuntimeState>): Promise<AgentRuntimeState> {
  const res = await api.put<AgentRuntimeState>(`/agents/${agentId}/runtime-state`, data);
  return res.data;
}

export async function accumulateUsage(agentId: string, data: { input_tokens: number; output_tokens: number; cost_cents: number }): Promise<void> {
  await api.post(`/agents/${agentId}/runtime-state/accumulate`, data);
}
