import axios from 'axios';
import { workspaceTransport } from './transport';

const api = axios.create({ baseURL: 'api/v1' });

export interface FileEntry {
  name: string;
  path: string;
  is_dir: boolean;
  size: number;
  mod_time: string;
}

export interface BrowseResult {
  path: string;
  parent?: string;
  entries: FileEntry[];
}

export async function browseFiles(path: string): Promise<BrowseResult> {
  const res = await api.get<BrowseResult>('/files/browse', { params: { path } });
  return res.data;
}

/** Explicit, nonsecret workspace selection also works in native media and new tabs. */
export function fileServeUrl(path: string, cacheKey?: string): string {
  const bust = cacheKey ? `&v=${encodeURIComponent(cacheKey)}` : '';
  const workspace = workspaceTransport.selected ? `&workspace_id=${encodeURIComponent(workspaceTransport.selected)}` : '';
  return `api/v1/files/serve?path=${encodeURIComponent(path)}${workspace}${bust}`;
}

/** Fetch a small server-side file (e.g. a JSON manifest) as text. */
export async function fetchFileText(path: string): Promise<string> {
  const res = await api.get('/files/serve', { params: { path }, responseType: 'text', transformResponse: [(d) => d] });
  return res.data as string;
}

export async function uploadFile(file: File, dir?: string, name?: string): Promise<{ path: string; size: number }> {
  const form = new FormData();
  form.append('file', file);
  if (dir) form.append('path', dir);
  if (name) form.append('name', name);
  const res = await api.post<{ path: string; size: number }>('/files/upload', form);
  return res.data;
}

export async function deleteFile(path: string): Promise<void> {
  await api.delete('/files', { params: { path } });
}
