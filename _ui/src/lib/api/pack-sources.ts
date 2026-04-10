import axios from 'axios';
import type { ListResult } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface PackSource {
  id: string;
  name: string;
  url: string;
  branch: string;
  status: string;
  last_sync?: string;
  error?: string;
  created_at: string;
  updated_at: string;
}

export async function listPackSources(): Promise<ListResult<PackSource>> {
  const res = await api.get<ListResult<PackSource>>('/pack-sources');
  return res.data;
}

export async function createPackSource(data: { name?: string; url: string; branch?: string }): Promise<PackSource> {
  const res = await api.post<PackSource>('/pack-sources', data);
  return res.data;
}

export async function deletePackSource(id: string): Promise<void> {
  await api.delete(`/pack-sources/${id}`);
}

export async function syncPackSource(id: string): Promise<PackSource> {
  const res = await api.post<PackSource>(`/pack-sources/${id}/sync`);
  return res.data;
}
