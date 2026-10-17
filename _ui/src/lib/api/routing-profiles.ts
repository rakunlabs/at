import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

/**
 * A named, ordered model chain. A gateway request whose `model` is the profile
 * name is expanded to `targets` and routed through the same fallback machinery
 * as an explicit `at_fallbacks` list.
 *
 * The name cannot contain "/": absence of a slash is what distinguishes a
 * profile name from a direct "provider/model" reference during routing.
 */
export interface RoutingProfile {
  owner_user_id?: string;
  scope?: 'personal' | 'workspace';
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  targets: string[];
  strategy: RoutingStrategy;
  /** Per-target weights for the weighted strategy (default 1, 0 = fallback only). */
  weights?: Record<string, number>;
  created_at: string;
  updated_at: string;
  created_by?: string;
  updated_by?: string;
}

export interface RoutingProfileInput {
  name: string;
  description?: string;
  targets: string[];
  strategy: RoutingStrategy;
  weights?: Record<string, number>;
}

/**
 * How a profile orders its targets for each request. Every strategy still
 * falls back through the remaining targets; providers that are cooling or
 * whose circuit is open are moved to the back afterwards.
 */
export type RoutingStrategy = 'priority' | 'round_robin' | 'weighted' | 'least_cost' | 'sticky';

export const ROUTING_STRATEGIES: { value: RoutingStrategy; label: string; description: string }[] = [
  { value: 'priority', label: 'Priority', description: 'Try targets in the order listed.' },
  { value: 'round_robin', label: 'Round robin', description: 'Rotate which target goes first on each request.' },
  { value: 'weighted', label: 'Weighted', description: 'Pick the first target at random by weight. Weight 0 makes a target fallback-only.' },
  { value: 'least_cost', label: 'Least cost', description: 'Cheapest priced model first. Unpriced models follow in listed order.' },
  { value: 'sticky', label: 'Sticky', description: 'Keep each session (or API token) on the same target so prompt caches stay warm.' },
];

export const ROUTING_PROFILE_MAX_TARGETS = 16;
export const ROUTING_PROFILE_MAX_WEIGHT = 1000;

// ─── API Functions ───

export async function listRoutingProfiles(params?: ListParams): Promise<ListResult<RoutingProfile>> {
  const res = await api.get<ListResult<RoutingProfile>>('/routing-profiles', { params });
  return res.data;
}

export async function getRoutingProfile(id: string): Promise<RoutingProfile> {
  const res = await api.get<RoutingProfile>(`/routing-profiles/${id}`);
  return res.data;
}

export async function createRoutingProfile(data: RoutingProfileInput): Promise<RoutingProfile> {
  const res = await api.post<RoutingProfile>('/routing-profiles', data);
  return res.data;
}

export async function updateRoutingProfile(id: string, data: RoutingProfileInput): Promise<RoutingProfile> {
  const res = await api.put<RoutingProfile>(`/routing-profiles/${id}`, data);
  return res.data;
}

export async function deleteRoutingProfile(id: string): Promise<void> {
  await api.delete(`/routing-profiles/${id}`);
}
