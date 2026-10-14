import type { RemotePluginDetail, RemotePluginMCP } from '../api/marketplace';

/** A marketplace source form normalised the way the server stores it. */
export interface SourceInput {
  url: string;
  ref: string;
  path: string;
}

/** Recognises what the user typed so the form can say what will be fetched. */
export function describeSourceURL(raw: string): string {
  const url = raw.trim();
  if (!url) return '';
  const [repo] = url.split('@');
  if (/^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/.test(repo)) return `GitHub repository github.com/${repo}`;
  if (/^https:\/\/.+\.json(\?.*)?$/i.test(url)) return 'marketplace.json file';
  if (/^(https|ssh):\/\//i.test(url) || /^[^/@\s]+@[^:\s]+:.+/.test(url)) return 'Git repository';
  return '';
}

/** Every ${VAR} placeholder across the chosen servers, sorted and unique. */
export function selectedVariables(detail: RemotePluginDetail | null, servers: string[]): string[] {
  if (!detail) return [];
  const names = new Set<string>();
  for (const mcp of detail.mcp_servers) {
    if (servers.includes(mcp.name)) for (const v of mcp.variables ?? []) names.add(v);
  }
  return [...names].sort();
}

/** Whether installing these servers needs the "runs a local command" confirmation. */
export function needsStdioConfirmation(detail: RemotePluginDetail | null, servers: string[]): boolean {
  return !!detail?.mcp_servers.some((m) => servers.includes(m.name) && m.type === 'stdio');
}

/** Servers AT can never install: they execute files shipped inside the plugin. */
export function installableMCP(mcp: RemotePluginMCP): boolean {
  return !mcp.plugin_relative;
}

/** One-line summary of an upstream for the selection list. */
export function mcpSummary(mcp: RemotePluginMCP): string {
  if (mcp.type === 'http') return mcp.upstream.url ?? '';
  return [mcp.upstream.command, ...(mcp.upstream.args ?? [])].filter(Boolean).join(' ');
}

/**
 * Variable mapping sent to the server. An AT variable is referenced as
 * "var:<key>" (resolved at call time, the secret never copied into the set);
 * anything else is stored literally. Blank entries are omitted so the
 * placeholder's own ${VAR:-default} applies.
 */
export function variableMapping(values: Record<string, { mode: 'var' | 'literal'; value: string }>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [name, entry] of Object.entries(values)) {
    const value = entry.value.trim();
    if (!value) continue;
    out[name] = entry.mode === 'var' ? `var:${value}` : value;
  }
  return out;
}
