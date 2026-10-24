import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

/** A question an MCP server asked during a tool call (MCP elicitation). */
export interface MCPElicitation {
  id: string;
  scope: string;
  server: string;
  /** form: answer in AT; url: open a page of the server's (AT never sees what is entered there). */
  mode: 'form' | 'url';
  message: string;
  requested_schema?: ElicitSchema;
  created_at: string;
  url?: string;
  host?: string;
  /** The host uses Punycode (possible look-alike domain). */
  punycode?: boolean;
  elicitation_id?: string;
  /** The tool call waits until the page is finished, then retries. */
  required?: boolean;
}

export interface ElicitProperty {
  type: 'string' | 'number' | 'integer' | 'boolean';
  title?: string;
  description?: string;
  enum?: string[];
  enumNames?: string[];
  default?: unknown;
  format?: string;
  minLength?: number;
  maxLength?: number;
  minimum?: number;
  maximum?: number;
}

export interface ElicitSchema {
  type: 'object';
  properties?: Record<string, ElicitProperty>;
  required?: string[];
}

export type ElicitAction = 'accept' | 'decline' | 'cancel';

/** Open questions; with wait, returns once one exists or after `wait` seconds. */
export async function listMCPElicitations(wait = 0, signal?: AbortSignal): Promise<MCPElicitation[]> {
  const res = await api.get<{ elicitations: MCPElicitation[] }>('/mcp/elicitations', { params: wait ? { wait } : undefined, signal });
  return res.data.elicitations ?? [];
}

export async function answerMCPElicitation(id: string, action: ElicitAction, content?: Record<string, unknown>): Promise<void> {
  await api.post(`/mcp/elicitations/${encodeURIComponent(id)}`, { action, ...(content ? { content } : {}) });
}
