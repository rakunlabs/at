import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Info API ───

export interface InfoProvider {
  shared?: boolean;
  key: string;
  reference?: string;
  scope?: 'personal' | 'workspace' | 'global';
  type: string;
  default_model: string;
  models: string[];
}

export interface InfoResponse {
  providers: InfoProvider[];
  store_type: string;
  name?: string;
  version?: string;
  commit?: string;
  build_date?: string;
  user?: string;
  // Effective task workspace base directory on the server filesystem.
  // Equals loopgov.Config.WorkspaceRoot, falling back to /tmp/at-tasks.
  workspace_root?: string;
  // Persistent asset library root (avatars, cloned voices) — ./data/assets.
  assets_root?: string;
}

export async function getInfo(workspaceID?: string): Promise<InfoResponse> {
  const config = workspaceID ? { headers: { 'X-AT-Workspace-ID': workspaceID } } : undefined;
  const res = await api.get<InfoResponse>('/info', config);
  return res.data;
}

// ─── Models API ───

export interface ModelData {
  id: string;
  object: string;
  owned_by: string;
}

interface ModelsResponse {
  object: string;
  data: ModelData[];
}

export async function listModels(authToken?: string): Promise<ModelData[]> {
  const headers: Record<string, string> = {};
  if (authToken) {
    headers['Authorization'] = `Bearer ${authToken}`;
  }
  const res = await api.get<ModelsResponse>('/models', { headers });
  return res.data.data;
}
