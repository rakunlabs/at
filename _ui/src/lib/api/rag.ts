import axios from 'axios';
import type { ListResult, ListParams } from './types';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export interface VectorStoreConfig {
  type: string;
  config: Record<string, any>;
}

export interface RAGCollectionConfig {
  description: string;
  vector_store: VectorStoreConfig;
  embedding_provider: string;
  embedding_model: string;
  embedding_url: string;
  embedding_api_type: string;
  embedding_bearer_auth: boolean;
  chunk_size: number;
  chunk_overlap: number;
}

export interface RAGCollection {
  id: string;
  name: string;
  config: RAGCollectionConfig;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
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

export interface SearchRequest {
  query: string;
  collection_ids?: string[];
  num_results?: number;
  score_threshold?: number;
}

// ─── Collection CRUD ───

export async function listCollections(params?: ListParams): Promise<ListResult<RAGCollection>> {
  const res = await api.get<ListResult<RAGCollection>>('/rag/collections', { params });
  return res.data;
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
  const res = await api.post<{ results: SearchResult[] }>('/rag/search', req);
  return res.data.results;
}

// ─── Embedding Model Discovery ───

export interface DiscoverEmbeddingModelsRequest {
  embedding_provider: string;
  embedding_api_type?: string;
  embedding_url?: string;
  embedding_bearer_auth?: boolean;
}

export async function discoverEmbeddingModels(req: DiscoverEmbeddingModelsRequest): Promise<string[]> {
  const res = await api.post<{ models: string[] }>('/rag/discover-embedding-models', req);
  return res.data.models;
}

// ─── Test Embedding ───

export interface TestEmbeddingRequest {
  embedding_provider: string;
  embedding_model?: string;
  embedding_url?: string;
  embedding_api_type?: string;
  embedding_bearer_auth?: boolean;
}

export interface TestEmbeddingResponse {
  success: boolean;
  model?: string;
  dimensions: number;
}

export async function testEmbedding(req: TestEmbeddingRequest): Promise<TestEmbeddingResponse> {
  const res = await api.post<TestEmbeddingResponse>('/rag/test-embedding', req);
  return res.data;
}

// ─── RAG MCP Servers ───

export interface RAGMCPServerConfig {
  description: string;
  collection_ids: string[];
  enabled_tools: string[];
  fetch_mode: string;
  git_cache_dir: string;
  default_num_results: number;
}

export interface RAGMCPServer {
  id: string;
  name: string;
  config: RAGMCPServerConfig;
  created_at: string;
  updated_at: string;
  created_by: string;
  updated_by: string;
}

export async function listRAGMCPServers(params?: ListParams): Promise<ListResult<RAGMCPServer>> {
  const res = await api.get<ListResult<RAGMCPServer>>('/rag/mcp-servers', { params });
  return res.data;
}

export async function getRAGMCPServer(id: string): Promise<RAGMCPServer> {
  const res = await api.get<RAGMCPServer>(`/rag/mcp-servers/${id}`);
  return res.data;
}

export async function createRAGMCPServer(data: Partial<RAGMCPServer>): Promise<RAGMCPServer> {
  const res = await api.post<RAGMCPServer>('/rag/mcp-servers', data);
  return res.data;
}

export async function updateRAGMCPServer(id: string, data: Partial<RAGMCPServer>): Promise<RAGMCPServer> {
  const res = await api.put<RAGMCPServer>(`/rag/mcp-servers/${id}`, data);
  return res.data;
}

export async function deleteRAGMCPServer(id: string): Promise<void> {
  await api.delete(`/rag/mcp-servers/${id}`);
}
