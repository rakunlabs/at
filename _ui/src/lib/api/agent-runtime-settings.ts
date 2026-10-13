import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface AgentRuntimeSettings {
  version: number;
  max_background_subagents_per_owner: number;
}

export const getAgentRuntimeSettings = async () =>
  (await api.get<AgentRuntimeSettings>('settings/agent-runtime')).data;

export const saveAgentRuntimeSettings = async (settings: AgentRuntimeSettings) =>
  (await api.put<AgentRuntimeSettings>('settings/agent-runtime', settings)).data;
