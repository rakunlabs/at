import axios from 'axios';
import type { ProviderQuotaConfig } from './providers';
import type { RoutingProfile } from './routing-profiles';

const api = axios.create({ baseURL: 'api/v1' });

export interface FreeTierEntry {
  id: string; name: string; type: string; base_url: string;
  signup_url: string; source_url: string; note: string;
  privacy: string; reviewed_at: string; model_hint: string;
}
export interface ProviderUsePolicy {
  block_free_tier: boolean;
  allowed_personal_hosts: string[] | null;
  version: number;
}
export interface QuotaUsage {
  model: string; period: string; reset_at: string;
  requests: number; tokens: number; reserved_tokens: number;
  request_limit: number; token_limit: number;
}
export async function freeTierCatalog(): Promise<FreeTierEntry[]> {
  return (await api.get('/personal-providers/free-tier-catalog')).data;
}
export async function providerUsePolicy(): Promise<ProviderUsePolicy> {
  return (await api.get('/personal-providers/policy')).data;
}
export async function saveProviderUsePolicy(policy: ProviderUsePolicy): Promise<ProviderUsePolicy> {
  return (await api.put('/personal-providers/policy', policy)).data;
}
export async function quotaUsage(key: string, personal: boolean): Promise<QuotaUsage[]> {
  return (await api.get(`/${personal ? 'personal-providers' : 'providers'}/${encodeURIComponent(key)}/quota`)).data;
}
export async function createPersonalProfile(name: string, targets: string[]): Promise<RoutingProfile> {
  return (await api.post('/personal-providers/routing-profiles', { name, targets, strategy: 'priority' })).data;
}
export async function findPersonalProfile(name: string): Promise<RoutingProfile | undefined> {
  const res = await api.get('/personal-providers/routing-profiles', { params: { name, _limit: 2 } });
  return res.data.data?.find((p: RoutingProfile) => p.name === name && !!p.owner_user_id);
}
export interface FreeTierDraft {
  entry: FreeTierEntry;
  selected: boolean;
  key: string;
  models: string;
  quota: ProviderQuotaConfig;
  rpm?: number;
  created_reference?: string;
  saved_key?: string;
  error?: string;
}
