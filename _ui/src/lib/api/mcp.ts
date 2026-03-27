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

export interface RAGToolDef {
  name: string;
  description: string;
  input_schema: Record<string, any>;
}

export interface RAGToolListResponse {
  tools: RAGToolDef[];
  available: boolean;
}

export interface RAGCallToolResponse {
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

/**
 * List available RAG tool definitions.
 * Returns tools + availability flag (false if RAG service not configured).
 */
export async function listRAGTools(): Promise<RAGToolListResponse> {
  const res = await api.get<RAGToolListResponse>('/mcp/rag-tools');
  return res.data;
}

/**
 * Optional git auth config for RAG tool calls.
 */
export interface RAGAuthConfig {
  token_variable?: string;
  token_user?: string;
  ssh_key_variable?: string;
}

/**
 * Call a RAG tool by name (rag_search, rag_list_collections, rag_fetch_source, rag_search_and_fetch, rag_search_and_fetch_org).
 */
export async function callRAGTool(
  name: string,
  args: Record<string, any>,
  auth?: RAGAuthConfig,
): Promise<RAGCallToolResponse> {
  const res = await api.post<RAGCallToolResponse>('/mcp/call-rag-tool', {
    name,
    arguments: args,
    ...(auth?.token_variable && { token_variable: auth.token_variable }),
    ...(auth?.token_user && { token_user: auth.token_user }),
    ...(auth?.ssh_key_variable && { ssh_key_variable: auth.ssh_key_variable }),
  });
  return res.data;
}
