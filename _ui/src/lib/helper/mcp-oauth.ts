import type { MCPUpstream, MCPUpstreamAuth, MCPAccountSource } from '../api/mcp-servers';

// Editor state for one HTTP upstream's Authentication section. Kept as plain
// strings so inputs can bind to it; `mcpUpstreamAuth` turns it back into the
// stored shape and is the only place that decides what gets saved.
export interface MCPAuthForm {
  enabled: boolean;
  provider: string;
  accounts: MCPAccountSource[];
  sharedConnectionID: string;
  scopes: string;
  clientID: string;
  authorizationServer: string;
}

export const MCP_ACCOUNT_SOURCES: MCPAccountSource[] = ['user', 'agent', 'shared'];

export const MCP_ACCOUNT_LABELS: Record<MCPAccountSource, string> = {
  user: 'Signed-in user',
  agent: 'Agent binding',
  shared: 'Shared account',
};

/** Mirrors mcpauth.ProviderForURL so the editor shows the key the server derives. */
export function mcpProviderForURL(raw: string): string {
  let host = '';
  try {
    host = new URL(raw.trim()).hostname.toLowerCase();
  } catch {
    host = '';
  }
  return ('mcp-' + host.replace(/[^a-z0-9]/g, '-')).replace(/-+$/, '');
}

export function mcpAuthForm(auth?: MCPUpstreamAuth): MCPAuthForm {
  return {
    enabled: !!auth,
    provider: auth?.provider ?? '',
    accounts: auth?.accounts?.length ? [...auth.accounts] : ['user'],
    sharedConnectionID: auth?.shared_connection_id ?? '',
    scopes: (auth?.scopes ?? []).join(' '),
    clientID: auth?.client_id ?? '',
    authorizationServer: auth?.authorization_server ?? '',
  };
}

export function mcpUpstreamAuth(form: MCPAuthForm): MCPUpstreamAuth | undefined {
  if (!form.enabled) return undefined;
  const auth: MCPUpstreamAuth = { type: 'oauth2' };
  const provider = form.provider.trim();
  if (provider) auth.provider = provider;
  const accounts = form.accounts.filter((a, i, all) => MCP_ACCOUNT_SOURCES.includes(a) && all.indexOf(a) === i);
  if (accounts.length && !(accounts.length === 1 && accounts[0] === 'user')) auth.accounts = accounts;
  if (accounts.includes('shared') && form.sharedConnectionID.trim()) auth.shared_connection_id = form.sharedConnectionID.trim();
  const scopes = form.scopes.split(/[\s,]+/).filter(Boolean);
  if (scopes.length) auth.scopes = [...new Set(scopes)];
  if (form.clientID.trim()) auth.client_id = form.clientID.trim();
  if (form.authorizationServer.trim()) auth.authorization_server = form.authorizationServer.trim();
  return auth;
}

/** Moves one account source up (-1) or down (+1) in the ordered list. */
export function moveAccountSource(accounts: MCPAccountSource[], index: number, delta: -1 | 1): MCPAccountSource[] {
  const target = index + delta;
  if (target < 0 || target >= accounts.length) return accounts;
  const next = [...accounts];
  [next[index], next[target]] = [next[target], next[index]];
  return next;
}

export function toggleAccountSource(accounts: MCPAccountSource[], source: MCPAccountSource): MCPAccountSource[] {
  return accounts.includes(source) ? accounts.filter(a => a !== source) : [...accounts, source];
}

/** The static Authorization header is replaced by OAuth; the server refuses both. */
export function withoutAuthorizationHeader(upstream: MCPUpstream): MCPUpstream {
  if (!upstream.headers) return upstream;
  const headers = Object.fromEntries(Object.entries(upstream.headers).filter(([k]) => k.trim().toLowerCase() !== 'authorization'));
  return { ...upstream, headers };
}

/** Build the create/update payload without losing HTTP network settings. */
export function mcpUpstreamsForSave(upstreams: MCPUpstream[]): MCPUpstream[] {
  return upstreams
    .filter(u => !!(u.url?.trim() || u.command?.trim()))
    .map(u => {
      if (u.command != null) {
        return { command: u.command.trim(), args: u.args, env: u.env };
      }
      // Keep HTTP fields together rather than rebuilding a partial allowlist
      // for each authentication mode. OAuth only changes the static headers.
      const { command, args, env, ...http } = u.auth ? withoutAuthorizationHeader(u) : u;
      return { ...http, url: u.url!.trim(), proxy: u.proxy?.trim() };
    });
}

/** Problems the editor reports before a save would be refused. */
export function mcpAuthProblems(upstream: MCPUpstream, form: MCPAuthForm): string[] {
  if (!form.enabled) return [];
  const problems: string[] = [];
  if (upstream.command) problems.push('OAuth applies to HTTP servers only.');
  if (!upstream.url?.trim()) problems.push('Enter the server URL first.');
  if (!form.accounts.length) problems.push('Choose at least one account source.');
  if (form.accounts.includes('shared') && !form.sharedConnectionID) problems.push('Choose the shared account.');
  if (Object.keys(upstream.headers ?? {}).some(k => k.trim().toLowerCase() === 'authorization')) problems.push('Remove the static Authorization header; OAuth supplies it.');
  const provider = form.provider.trim();
  if (provider && !/^[a-z0-9_-]{2,64}$/.test(provider)) problems.push('Provider key: 2-64 lowercase letters, digits, - or _.');
  return problems;
}
