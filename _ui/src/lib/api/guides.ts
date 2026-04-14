import axios from 'axios';
import type { ListResult } from './types';

const api = axios.create({ baseURL: 'api/v1' });

export interface Guide {
  id: string;
  title: string;
  description: string;
  icon: string;
  content: string;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

export interface GuideInput {
  title: string;
  description: string;
  icon: string;
  content: string;
}

export async function listGuides(): Promise<ListResult<Guide>> {
  const res = await api.get<ListResult<Guide>>('/guides');
  return res.data;
}

export async function getGuide(id: string): Promise<Guide> {
  const res = await api.get<Guide>(`/guides/${id}`);
  return res.data;
}

export async function createGuide(data: GuideInput): Promise<Guide> {
  const res = await api.post<Guide>('/guides', data);
  return res.data;
}

export async function updateGuide(id: string, data: GuideInput): Promise<Guide> {
  const res = await api.put<Guide>(`/guides/${id}`, data);
  return res.data;
}

export async function deleteGuide(id: string): Promise<void> {
  await api.delete(`/guides/${id}`);
}
