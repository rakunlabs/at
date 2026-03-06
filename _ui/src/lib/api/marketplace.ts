import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface MarketplaceSource {
  id: string;
  name: string;
  type: string;
  search_url: string;
  top_url: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface MarketplaceSkill {
  source: string;
  slug: string;
  name: string;
  description: string;
  author: string;
  downloads: number;
  license: string;
  tags: string[];
  url: string;
}

// ─── Source Management ───

export async function listMarketplaceSources(): Promise<MarketplaceSource[]> {
  const res = await api.get<MarketplaceSource[]>('/marketplace/sources');
  return res.data;
}

export async function createMarketplaceSource(src: Partial<MarketplaceSource>): Promise<MarketplaceSource> {
  const res = await api.post<MarketplaceSource>('/marketplace/sources', src);
  return res.data;
}

export async function updateMarketplaceSource(id: string, src: Partial<MarketplaceSource>): Promise<MarketplaceSource> {
  const res = await api.put<MarketplaceSource>(`/marketplace/sources/${id}`, src);
  return res.data;
}

export async function deleteMarketplaceSource(id: string): Promise<void> {
  await api.delete(`/marketplace/sources/${id}`);
}

// ─── Search and Import ───

export async function searchMarketplace(query: string, source?: string): Promise<{ skills: MarketplaceSkill[] }> {
  const params: any = {};
  if (query) params.q = query;
  if (source) params.source = source;
  const res = await api.get<{ skills: MarketplaceSkill[] }>('/marketplace/search', { params });
  return res.data;
}

export async function getTopSkills(source?: string): Promise<{ skills: MarketplaceSkill[] }> {
  const params: any = {};
  if (source) params.source = source;
  const res = await api.get<{ skills: MarketplaceSkill[] }>('/marketplace/top', { params });
  return res.data;
}

export async function previewMarketplaceSkill(url: string): Promise<any> {
  const res = await api.post('/marketplace/preview', { url });
  return res.data;
}

export async function importMarketplaceSkill(url: string): Promise<any> {
  const res = await api.post('/marketplace/import', { url });
  return res.data;
}
