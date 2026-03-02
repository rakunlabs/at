import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface VectorStoreConfig {
  type: string;
  config: Record<string, any>;
}

export interface RAGCollection {
  id: string;
  name: string;
  description: string;
  vector_store: VectorStoreConfig;
  embedding_provider: string;
  embedding_model: string;
  embedding_url: string;
  embedding_api_type: string;
  chunk_size: number;
  chunk_overlap: number;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

interface CollectionsResponse {
  collections: RAGCollection[];
}

export interface UploadResult {
  chunks_stored: number;
  source: string;
}

export interface SearchResult {
  content: string;
  metadata: Record<string, any>;
  score: number;
  collection_id: string;
}

interface SearchResponse {
  results: SearchResult[];
}

export interface SearchRequest {
  query: string;
  collection_ids?: string[];
  num_results?: number;
  score_threshold?: number;
}

// ─── Collection CRUD ───

export async function listCollections(): Promise<RAGCollection[]> {
  const res = await api.get<CollectionsResponse>('/rag/collections');
  return res.data.collections;
}

export async function getCollection(id: string): Promise<RAGCollection> {
  const res = await api.get<RAGCollection>(`/rag/collections/${id}`);
  return res.data;
}

export async function createCollection(data: Partial<RAGCollection>): Promise<RAGCollection> {
  const res = await api.post<RAGCollection>('/rag/collections', data);
  return res.data;
}

export async function updateCollection(id: string, data: Partial<RAGCollection>): Promise<RAGCollection> {
  const res = await api.put<RAGCollection>(`/rag/collections/${id}`, data);
  return res.data;
}

export async function deleteCollection(id: string): Promise<void> {
  await api.delete(`/rag/collections/${id}`);
}

// ─── Document Upload ───

export async function uploadDocument(collectionId: string, file: File): Promise<UploadResult> {
  const form = new FormData();
  form.append('file', file);
  const res = await api.post<UploadResult>(`/rag/collections/${collectionId}/documents`, form);
  return res.data;
}

export async function importFromURL(collectionId: string, url: string, contentType?: string): Promise<UploadResult> {
  const res = await api.post<UploadResult>(`/rag/collections/${collectionId}/import/url`, {
    url,
    content_type: contentType,
  });
  return res.data;
}

// ─── Search ───

export async function searchRAG(req: SearchRequest): Promise<SearchResult[]> {
  const res = await api.post<SearchResponse>('/rag/search', req);
  return res.data.results;
}
