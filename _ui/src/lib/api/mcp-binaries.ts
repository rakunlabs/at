import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

// An entry in the persistent MCP program library (<workspace root>/mcps or
// ./data/mcps): binaries referenced by stdio upstream commands, config files
// referenced via env vars, or a directory extracted from an uploaded archive
// (.tar.gz / .tgz / .tar). Survives restarts; janitor-exempt.
export interface MCPBinary {
  name: string;
  size: number;
  mode: string;
  executable: boolean;
  modified_at: string;
  dir?: boolean; // extracted archive root
  entries?: number; // direct children of a directory
}

export interface MCPBinaryList {
  dir: string;
  files: MCPBinary[];
}

// ─── API ───

export async function listMCPBinaries(): Promise<MCPBinaryList> {
  const res = await api.get<MCPBinaryList>('/mcp/binaries');
  return res.data;
}

// Archives (.tar.gz / .tgz / .tar) are extracted server-side into a directory
// named after the archive unless extract=false is passed.
export async function uploadMCPBinary(
  file: File,
  opts?: { name?: string; executable?: boolean; extract?: boolean },
): Promise<{ name: string; path: string; size: number; executable?: boolean; files?: number; extracted?: boolean }> {
  const form = new FormData();
  form.append('file', file);
  if (opts?.name) form.append('name', opts.name);
  if (opts?.executable !== undefined) form.append('executable', String(opts.executable));
  if (opts?.extract !== undefined) form.append('extract', String(opts.extract));
  const res = await api.post('/mcp/binaries', form);
  return res.data;
}

export async function deleteMCPBinary(name: string): Promise<void> {
  await api.delete(`/mcp/binaries/${encodeURIComponent(name)}`);
}

// ─── Stdio process status (global) ───

export interface StdioProcess {
  command: string;
  pid: number;
  alive: boolean;
  started_at: string;
  uptime_seconds: number;
  exit_error?: string;
}

export async function listStdioProcesses(): Promise<StdioProcess[]> {
  const res = await api.get<{ processes: StdioProcess[] }>('/mcp/stdio-processes');
  return res.data.processes || [];
}
