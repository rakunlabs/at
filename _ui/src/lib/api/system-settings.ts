import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface KubernetesSandbox {
  namespace: string;
  deployment_id: string;
  kubeconfig: string;
  helper_image: string;
  storage_class: string;
  home_storage_class: string;
  home_access_mode: string;
  home_size: string;
  workspace_size: string;
  runtime_class: string;
  pod_pids_limit: number;
  network_policy_enforced: boolean;
  single_replica: boolean;
  blocked_cidrs: string[];
}

export interface SystemSettings {
  version: number;
  name: string;
  external_url: string;
  log_level: string;
  workspace_ttl_hours: number;
  sandbox: { backend: string; kubernetes?: KubernetesSandbox };
}

export interface SystemSettingsResponse {
  settings: SystemSettings;
  active: SystemSettings;
  runtime: { backend: string; notice?: string };
  apply_error?: string;
}

export async function getSystemSettings(): Promise<SystemSettingsResponse> {
  return (await api.get<SystemSettingsResponse>('settings/system')).data;
}

export async function saveSystemSettings(settings: SystemSettings, confirm_stop: boolean, single_replica: boolean): Promise<SystemSettingsResponse> {
  // The server bounds draining at two minutes. Never retry this write implicitly.
  return (await api.put<SystemSettingsResponse>('settings/system', { settings, confirm_stop, single_replica }, { timeout: 150_000 })).data;
}
