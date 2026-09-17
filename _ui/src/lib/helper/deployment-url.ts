// Absolute URLs that the user copies out of the UI — webhook endpoints, MCP
// endpoints, Claude Code marketplace links — must carry the deployment's
// base path.
//
// `location.origin` and `location.host` drop it, which silently produces
// `https://host/webhooks/x` for an instance served at `https://host/at/`. The
// server registers every one of those routes under the prefix, so the copied
// URL 404s. `document.baseURI` is the only value that already carries the
// prefix regardless of where the SPA router currently is, because the built
// index.html is served from the base directory and all assets are relative.

/** Base directory of the deployment, always ending in a slash. */
export function deploymentBase(): URL {
  return new URL('.', document.baseURI);
}

/**
 * Absolute URL for a server route, given a path relative to the deployment
 * base (no leading slash). Returns an `http(s)` URL.
 */
export function deploymentUrl(path: string): string {
  return new URL(path.replace(/^\/+/, ''), deploymentBase()).href;
}

/**
 * Same as deploymentUrl but with the scheme swapped to ws/wss, for endpoints
 * that are upgraded to a WebSocket.
 */
export function deploymentWsUrl(path: string): string {
  const url = new URL(path.replace(/^\/+/, ''), deploymentBase());
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  return url.href;
}

/** Deployment root without a trailing slash, for string concatenation. */
export function deploymentOrigin(): string {
  return deploymentUrl('').replace(/\/+$/, '');
}
