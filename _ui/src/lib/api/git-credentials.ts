import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

export interface GitCredential {
  id: string;
  name: string;
  host: string;
  port: number;
  public_key: string;
  fingerprints: string[];
  created_at: string;
  updated_at: string;
}

export interface GitHostScan {
  host: string;
  port: number;
  fingerprints: string[];
}

export async function listGitCredentials(): Promise<GitCredential[]> {
  return (await api.get<GitCredential[]>('/git-credentials')).data;
}

export async function scanGitHost(host: string, port: number): Promise<GitHostScan> {
  return (await api.post<GitHostScan>('/git-credentials/scan-host', { host, port })).data;
}

export async function createGitCredential(input: { name: string; host: string; port: number; fingerprints: string[] }): Promise<GitCredential> {
  return (await api.post<GitCredential>('/git-credentials', input)).data;
}

export async function rotateGitCredential(id: string): Promise<GitCredential> {
  return (await api.post<GitCredential>(`/git-credentials/${id}/rotate`)).data;
}

export async function testGitCredential(id: string, repository_url: string): Promise<void> {
  await api.post(`/git-credentials/${id}/test`, { repository_url }, { timeout: 25000 });
}

export async function deleteGitCredential(id: string): Promise<void> {
  await api.delete(`/git-credentials/${id}`);
}
