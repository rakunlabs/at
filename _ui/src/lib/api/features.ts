import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export const FEATURE_PROVIDER_SETUP = 'provider_setup';
export const FEATURE_CHAT_WORKBENCH = 'chat_workbench';
export const FEATURE_AGENTS = 'agents';
export const FEATURE_AUTOMATION = 'automation';
export const FEATURE_RAG = 'rag';
export const FEATURE_FILES = 'files';
export const FEATURE_CONNECTIONS = 'connections_integrations';
export const FEATURE_ORGANIZATION_WORKFLOWS = 'organization_workflows';

export interface Feature {
  key: string;
  name: string;
  description: string;
  group: string;
  group_name: string;
  group_description: string;
  enabled: boolean;
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

export interface FeaturesResponse {
  groups: FeatureGroup[];
  features: Feature[];
}

export async function listFeatures(): Promise<FeaturesResponse> {
  const res = await api.get<FeaturesResponse>('/features');
  return res.data;
}

export async function updateFeature(key: string, enabled: boolean): Promise<Feature> {
  const res = await api.put<Feature>(`/features/${key}`, { enabled });
  return res.data;
}
