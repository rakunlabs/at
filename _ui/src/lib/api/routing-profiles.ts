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
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  targets: string[];
  created_at: string;
  updated_at: string;
  created_by?: string;
  updated_by?: string;
}

export interface RoutingProfileInput {
  name: string;
  description?: string;
  targets: string[];
}

export const ROUTING_PROFILE_MAX_TARGETS = 16;

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
