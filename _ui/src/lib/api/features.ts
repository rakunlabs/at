import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

// Mirrors internal/service/types-feature.go. The first block are the original
// coarse keys, which are now parents: a child is only reachable when its whole
// ancestor chain is enabled.
export const FEATURE_PROVIDER_SETUP = 'provider_setup';
export const FEATURE_CHAT_WORKBENCH = 'chat_workbench';
export const FEATURE_AGENTS = 'agents';
export const FEATURE_AUTOMATION = 'automation';
export const FEATURE_FILES = 'files';
export const FEATURE_CONNECTIONS = 'connections_integrations';
export const FEATURE_ORGANIZATION_WORKFLOWS = 'organization_workflows';
export const FEATURE_LLM_AUDIT = 'llm_audit';

export const FEATURE_MODEL_PRICING = 'model_pricing';
export const FEATURE_USAGE_ANALYTICS = 'usage_analytics';
export const FEATURE_API_TOKENS = 'api_tokens';
export const FEATURE_ROUTING_PROFILES = 'routing_profiles';
export const FEATURE_PLAYGROUND = 'playground';
export const FEATURE_CHAT_LOCAL_MCP = 'chat_local_mcp';
export const FEATURE_CHAT_EXTENSIONS = 'chat_extensions';
export const FEATURE_CHAT_SESSIONS = 'chat_sessions';
export const FEATURE_BOTS = 'bots';
export const FEATURE_AUDIO_TRANSCRIPTION = 'audio_transcription';
export const FEATURE_AGENT_HEARTBEATS = 'agent_heartbeats';
export const FEATURE_SKILLS = 'skills';
export const FEATURE_MARKETPLACES = 'marketplaces';
export const FEATURE_GUIDES = 'guides';
export const FEATURE_VARIABLES = 'variables';
export const FEATURE_WORKFLOW_BUILDER = 'workflow_builder';
export const FEATURE_WORKFLOW_RUNS = 'workflow_runs';
export const FEATURE_WEBHOOK_TRIGGERS = 'webhook_triggers';
export const FEATURE_CRON_TRIGGERS = 'cron_triggers';
export const FEATURE_MCP_SERVERS = 'mcp_servers';
export const FEATURE_MCP_TOOLS = 'mcp_tools';
export const FEATURE_BUILTIN_TOOLS = 'builtin_tools';
export const FEATURE_BUILTIN_SHELL = 'builtin_shell';
export const FEATURE_BUILTIN_SCRIPT = 'builtin_script';
export const FEATURE_BUILTIN_HTTP = 'builtin_http';
export const FEATURE_BUILTIN_OTHER = 'builtin_other';
export const FEATURE_TERMINAL = 'terminal';
export const FEATURE_EXTERNAL_CONNECTIONS = 'external_connections';
export const FEATURE_INTEGRATION_PACKS = 'integration_packs';
export const FEATURE_ORGANIZATIONS = 'organizations';
export const FEATURE_TASKS = 'tasks';
export const FEATURE_GOALS_PROJECTS = 'goals_projects';
export const FEATURE_APPROVALS = 'approvals';
export const FEATURE_STUDIO = 'studio';
export const FEATURE_LLM_TRACES = 'llm_traces';
export const FEATURE_WORKSPACE_MANAGEMENT = 'workspace_management';

export interface Feature {
  key: string;
  name: string;
  description: string;
  group: string;
  group_name: string;
  group_description: string;
  parent?: string;
  /** This feature's own switch position. */
  enabled: boolean;
  /** enabled AND every ancestor enabled — what the gate actually applies. */
  effective: boolean;
  /** Closest disabled ancestor, when one is blocking this feature. */
  blocked_by?: string;
  created_at?: string;
  updated_at?: string;
  created_by?: string;
  updated_by?: string;
}

export interface FeatureGroup {
  key: string;
  name: string;
  description: string;
  features: Feature[];
}

export interface FeaturePreset {
  key: string;
  name: string;
  description: string;
  enabled: string[] | null;
}

export interface FeaturesResponse {
  groups: FeatureGroup[];
  features: Feature[];
  presets: FeaturePreset[];
}

export async function listFeatures(): Promise<FeaturesResponse> {
  const res = await api.get<FeaturesResponse>('/features');
  return res.data;
}

export async function updateFeature(key: string, enabled: boolean): Promise<Feature> {
  const res = await api.put<Feature>(`/features/${key}`, { enabled });
  return res.data;
}

/** Writes several keys in one request and returns the whole refreshed catalog. */
export async function updateFeatures(features: Record<string, boolean>): Promise<FeaturesResponse> {
  const res = await api.put<FeaturesResponse>('/features', { features });
  return res.data;
}

export async function applyFeaturePreset(preset: string): Promise<FeaturesResponse> {
  const res = await api.post<FeaturesResponse>(`/features/presets/${preset}`);
  return res.data;
}
