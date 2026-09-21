/**
 * Local MCP servers: MCP endpoints running on the person's own computer,
 * dialled by this browser rather than by AT.
 *
 * Every other MCP path in the product connects from the server process, so a
 * loopback URL there is the server's loopback. Here it is the user's, which is
 * the whole point — and also why nothing in this module ever asks the server
 * to make the request.
 */

export interface LocalMCPServer {
  id: string;
  name: string;
  url: string;
  /** Redacted to '***' on ordinary reads; revealed per record before dialling. */
  headers?: Record<string, string>;
  created_at?: string;
  updated_at?: string;
}

/** The redaction sentinel the rest of the product uses. */
export const REDACTED = '***';

/**
 * Mirrors service.LocalMCPHostAllowed. Re-checked here because a record may
 * have been written by an older client, and because the request is made from
 * this page: the server cannot refuse what it never sees.
 */
export function isLocalMCPHostAllowed(host: string): boolean {
  const h = host.trim().toLowerCase().replace(/^\[|\]$/g, '');
  if (!h) return false;
  if (h === 'localhost' || h.endsWith('.localhost')) return true;
  if (h.endsWith('.local')) return true;

  const v4 = h.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/);
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])];
    if (v4.slice(1).some(p => Number(p) > 255)) return false;
    if (a === 127) return true;                    // loopback
    if (a === 10) return true;                     // RFC1918
    if (a === 192 && b === 168) return true;       // RFC1918
    if (a === 172 && b >= 16 && b <= 31) return true; // RFC1918
    if (a === 100 && b >= 64 && b <= 127) return true; // CGNAT / overlay networks
    if (a === 169 && b === 254) return true;       // link-local
    return false;
  }

  if (h.includes(':')) {
    if (h === '::1') return true;
    // Unique-local and link-local IPv6.
    if (/^f[cd][0-9a-f]{2}:/.test(h)) return true;
    if (/^fe[89ab][0-9a-f]:/.test(h)) return true;
    return false;
  }

  return false;
}

export function localMCPUrlProblem(url: string): string {
  const trimmed = url.trim();
  if (!trimmed) return 'URL is required';
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return 'Enter a full URL, for example http://127.0.0.1:3000/mcp';
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') {
    return 'URL must use http or https';
  }
  if (parsed.username || parsed.password) {
    return 'Put credentials in a header, not in the URL';
  }
  if (!isLocalMCPHostAllowed(parsed.hostname)) {
    return `${parsed.hostname} is not a local address. Register a reachable MCP server as an MCP set instead, where execution policy and tracing apply.`;
  }

  return '';
}

/**
 * A local server must not be able to take over the name of a built-in or
 * MCP-set tool the conversation already relies on, so local tools are
 * registered last and yield the name on collision.
 */
export function localMCPToolName(tool: string, serverName: string, taken: (name: string) => boolean): string {
  if (!taken(tool)) return tool;
  const prefix = serverName.trim().toLowerCase().replace(/[^a-z0-9_-]+/g, '_').replace(/^_+|_+$/g, '') || 'local';
  let qualified = `${prefix}__${tool}`;
  let n = 2;
  while (taken(qualified)) {
    qualified = `${prefix}${n}__${tool}`;
    n += 1;
  }

  return qualified;
}

/**
 * Chats is not governed by loopgov — that governs the three server-side loops
 * — so nothing else would stop a local tool result from filling the context
 * window. There is no workspace to spill the rest into, so the notice asks for
 * a narrower result instead of pointing at a file.
 */
export const LOCAL_TOOL_RESULT_MAX = 65536;

export function clipLocalToolResult(text: string, max = LOCAL_TOOL_RESULT_MAX): string {
  if (text.length <= max) return text;

  return (
    text.slice(0, max) +
    `\n\n[truncated: the tool returned ${text.length} characters, ${max} shown. ` +
    `Call it again with a narrower request.]`
  );
}

/**
 * Approval is stored per device, not on the record.
 *
 * `http://127.0.0.1:3000/mcp` is a different program on a laptop and on a
 * desktop. A synced approval would therefore authorize, on the second machine,
 * a server the person inspected on the first — which is the one thing an
 * approval is meant to prevent.
 */
const APPROVAL_KEY = 'at.local-mcp.approved';

export interface LocalMCPApproval {
  /** Tool names present when the server was approved, to label later additions. */
  tools: string[];
  at: string;
}

type ApprovalMap = Record<string, LocalMCPApproval>;

function readApprovals(storage: Storage | undefined): ApprovalMap {
  if (!storage) return {};
  try {
    const raw = storage.getItem(APPROVAL_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);

    return parsed && typeof parsed === 'object' ? parsed as ApprovalMap : {};
  } catch {
    return {};
  }
}

function writeApprovals(storage: Storage | undefined, map: ApprovalMap): void {
  if (!storage) return;
  try {
    storage.setItem(APPROVAL_KEY, JSON.stringify(map));
  } catch {
    // A full or disabled storage means the approval does not persist; the
    // user is asked again next time, which is the safe direction.
  }
}

export function approvalFor(id: string, storage: Storage | undefined): LocalMCPApproval | null {
  return readApprovals(storage)[id] ?? null;
}

export function approveLocalMCP(id: string, tools: string[], storage: Storage | undefined): void {
  const map = readApprovals(storage);
  map[id] = { tools: [...tools], at: new Date().toISOString() };
  writeApprovals(storage, map);
}

export function revokeLocalMCP(id: string, storage: Storage | undefined): void {
  const map = readApprovals(storage);
  delete map[id];
  writeApprovals(storage, map);
}

/** Tool names this server has gained since it was approved on this device. */
export function toolsAddedSinceApproval(approval: LocalMCPApproval | null, tools: string[]): string[] {
  if (!approval) return [];
  const known = new Set(approval.tools);

  return tools.filter(name => !known.has(name));
}
