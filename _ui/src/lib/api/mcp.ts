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

export interface SkillCallToolResponse {
  result: string;
  error?: string;
}

export interface BuiltinToolDef {
  name: string;
  description: string;
  input_schema: Record<string, any>;
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
 * Call a skill tool handler via the backend.
 * Looks up the skill by name, finds the tool, and executes its handler.
 */
export async function callSkillTool(
  skillName: string,
  toolName: string,
  args: Record<string, any>,
): Promise<SkillCallToolResponse> {
  const res = await api.post<SkillCallToolResponse>('/mcp/call-skill-tool', {
    skill_name: skillName,
    tool_name: toolName,
    arguments: args,
  });
  return res.data;
}

/**
 * List available server-side built-in tool definitions.
 */
export async function listBuiltinTools(): Promise<BuiltinToolListResponse> {
  const res = await api.get<BuiltinToolListResponse>('/mcp/builtin-tools');
  return res.data;
}

/**
 * Call a server-side built-in tool by name.
 */
export async function callBuiltinTool(
  name: string,
  args: Record<string, any>,
): Promise<BuiltinCallToolResponse> {
  const res = await api.post<BuiltinCallToolResponse>('/mcp/call-builtin-tool', {
    name,
    arguments: args,
  });
  return res.data;
}
