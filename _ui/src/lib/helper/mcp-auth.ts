/** The OAuth parameters the consent page forwards, taken from its hash query. */
export const MCP_AUTH_PARAMS = ['response_type', 'client_id', 'redirect_uri', 'state', 'code_challenge', 'code_challenge_method', 'resource', 'scope'] as const;

export function mcpAuthParams(query: string): Record<string, string> {
  const q = new URLSearchParams(query);
  const out: Record<string, string> = {};
  for (const key of MCP_AUTH_PARAMS) {
    const v = q.get(key);
    if (v !== null) out[key] = v;
  }
  return out;
}

/**
 * Where the browser may be sent after a decision. The server builds this
 * from the client's registered redirect URI; it is still checked here so a
 * compromised response cannot navigate to a script URL.
 */
export function safeMCPAuthRedirect(raw: string): string | null {
  try {
    const u = new URL(raw);
    return u.protocol === 'https:' || u.protocol === 'http:' ? u.href : null;
  } catch {
    return null;
  }
}
