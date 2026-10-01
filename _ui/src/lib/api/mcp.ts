import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

// ─── Types ───

export interface MCPToolInfo {
  name: string;
  description: string;
  input_schema: Record<string, any>;
  server_url: string;
}

export interface MCPListToolsResponse {
  tools: MCPToolInfo[];
  errors?: string[];
}

export interface MCPCallToolResponse {
  result: string;
  error?: string;
}

export interface BuiltinToolDef {
  name: string;
  description: string;
  input_schema: Record<string, any>;
  family?: string;
  group?: string;
  disabled_by?: string;
}

export interface BuiltinToolListResponse {
  tools: BuiltinToolDef[];
}

export interface BuiltinCallToolResponse {
  result: string;
  error?: string;
}

// ─── API Functions ───

/**
 * Discover tools from one or more MCP servers via the backend proxy.
 * Returns merged tool list from all reachable servers.
 */
export async function listMCPTools(
  urls: string[],
  headers?: Record<string, string>,
): Promise<MCPListToolsResponse> {
  const res = await api.post<MCPListToolsResponse>('/mcp/list-tools', {
    urls,
    ...(headers && Object.keys(headers).length > 0 ? { headers } : {}),
  });
  return res.data;
}

/**
 * Call a tool on an MCP server via the backend proxy.
 */
export async function callMCPTool(
  serverUrl: string,
  name: string,
  args: Record<string, any>,
  headers?: Record<string, string>,
): Promise<MCPCallToolResponse> {
  const res = await api.post<MCPCallToolResponse>('/mcp/call-tool', {
    server_url: serverUrl,
    name,
    arguments: args,
    ...(headers && Object.keys(headers).length > 0 ? { headers } : {}),
  });
  return res.data;
}

/**
 * List available server-side built-in tool definitions.
 */
export async function listBuiltinTools(includeDisabled = false): Promise<BuiltinToolListResponse> {
  const res = await api.get<BuiltinToolListResponse>('/mcp/builtin-tools', {
    params: includeDisabled ? { include_disabled: true } : undefined,
  });
  return res.data;
}

/**
 * Call a server-side built-in tool by name.
 */
export async function callBuiltinTool(
  name: string,
  args: Record<string, any>,
  agentId = '',
  traceId = '',
  sessionId = '',
  signal?: AbortSignal,
): Promise<BuiltinCallToolResponse> {
  const res = await api.post<BuiltinCallToolResponse>('/mcp/call-builtin-tool', {
    name,
    arguments: args,
    agent_id: agentId || undefined,
    trace_id: traceId || undefined,
  }, { ...(sessionId ? { headers: { 'X-Session-ID': sessionId } } : {}), ...(signal ? { signal } : {}) });
  return res.data;
}

export interface SkillRunArtifact {
  media_id: string;
  name: string;
  content_type: string;
  size_bytes: number;
}

export interface SkillRunStatus {
  run_id: string;
  status: 'queued' | 'running' | 'cancelling' | 'completed' | 'failed' | 'cancelled';
  agent_name: string;
  result?: string;
  error?: string;
  artifacts?: SkillRunArtifact[];
  artifacts_note?: string;
}

/**
 * Run an agent-bound (context: fork) skill. Foreground answers with the
 * subagent's result; background answers with a run ID for waitSkillRun.
 */
export async function runSkill(
  body: { skill: string; task: string; context?: string; background?: boolean; trace_id?: string },
  signal?: AbortSignal,
): Promise<BuiltinCallToolResponse> {
  const res = await api.post<BuiltinCallToolResponse>('/chats/skill-runs', body, { signal });
  return res.data;
}

/** Long-polls one background skill run for up to `waitSeconds`. */
export async function waitSkillRun(runId: string, waitSeconds = 20, signal?: AbortSignal): Promise<SkillRunStatus> {
  const res = await api.get<SkillRunStatus>(`/chats/skill-runs/${encodeURIComponent(runId)}`, { params: { wait: waitSeconds }, signal });
  return res.data;
}
