/**
 * Local providers: OpenAI-compatible endpoints the browser calls directly from
 * Chats — a model server on this computer or a hosted API with the person's
 * own key. The server stores the record and never sends a request to it.
 */

import { isLocalMCPHostAllowed } from './local-mcp';

/** Model references served by a local provider: `local:<name>/<model>`. */
export const LOCAL_PROVIDER_PREFIX = 'local:';

export function isLocalModelRef(ref: string): boolean {
  return ref.startsWith(LOCAL_PROVIDER_PREFIX);
}

export function localModelRef(provider: string, model: string): string {
  return `${LOCAL_PROVIDER_PREFIX}${provider}/${model}`;
}

/** Splits `local:<name>/<model>`; the model half may contain slashes. */
export function parseLocalModelRef(ref: string): { provider: string; model: string } | null {
  if (!isLocalModelRef(ref)) return null;
  const rest = ref.slice(LOCAL_PROVIDER_PREFIX.length);
  const at = rest.indexOf('/');
  if (at <= 0 || at === rest.length - 1) return null;

  return { provider: rest.slice(0, at), model: rest.slice(at + 1) };
}

const NAME_RE = /^[a-z0-9][a-z0-9._-]{0,31}$/;

/** Mirrors service.NormalizeLocalChatProviders for the name. */
export function localProviderNameProblem(name: string): string {
  const n = name.trim().toLowerCase();
  if (!n) return 'Name is required';
  if (!NAME_RE.test(n)) return 'Name must be 1-32 lowercase letters, digits, dots, underscores or hyphens';

  return '';
}

/**
 * Mirrors service.ValidateLocalChatProviderURL. Plain http only for local
 * addresses: an API key sent over http to a public host is readable on the
 * way, and an https page cannot call one anyway.
 */
export function localProviderUrlProblem(url: string): string {
  const trimmed = url.trim();
  if (!trimmed) return 'Base URL is required';
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return 'Enter a full URL, for example http://127.0.0.1:11434/v1';
  }
  if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return 'Base URL must use http or https';
  if (parsed.username || parsed.password) return 'Put credentials in the API key or a header, not in the URL';
  if (parsed.search || parsed.hash) return 'Base URL must not contain a query or fragment';
  if (parsed.protocol === 'http:' && !isLocalMCPHostAllowed(parsed.hostname)) {
    return `${parsed.hostname} is not a local address; use https`;
  }

  return '';
}

/** Joins the API root and a path without doubling or dropping the slash. */
export function localProviderEndpoint(baseUrl: string, path: string): string {
  return `${baseUrl.trim().replace(/\/+$/, '')}/${path.replace(/^\/+/, '')}`;
}

export function localProviderHeaders(secrets: { api_key: string; headers: Record<string, string> }): Record<string, string> {
  const out: Record<string, string> = { ...secrets.headers };
  const hasAuth = Object.keys(out).some(k => k.toLowerCase() === 'authorization');
  if (secrets.api_key && !hasAuth) out.Authorization = `Bearer ${secrets.api_key}`;

  return out;
}

export const LOCAL_PROVIDER_CORS_HINT =
  'Your browser calls this provider directly, so it must allow this page: ' +
  'Access-Control-Allow-Origin for this origin and Access-Control-Allow-Headers including ' +
  'authorization and content-type. For a local address reached from a page that is not local, ' +
  'Chrome also requires Access-Control-Allow-Private-Network: true on the preflight. ' +
  'Ollama: set OLLAMA_ORIGINS; LM Studio: enable CORS in the server settings.';

/**
 * A browser reports a refused connection and a refused cross-origin request
 * identically (`TypeError: Failed to fetch`), so the message names both.
 */
export function describeLocalProviderError(e: unknown, name: string): Error {
  if (e instanceof TypeError) {
    return new Error(`Could not reach local provider "${name}": the server is not running or it does not allow this page. ${LOCAL_PROVIDER_CORS_HINT}`);
  }

  return e instanceof Error ? e : new Error(String(e));
}

/** Reads the model IDs from an OpenAI `GET /models` response. */
export function parseModelList(body: unknown): string[] {
  const data = (body as { data?: unknown })?.data;
  if (!Array.isArray(data)) throw new Error('The provider did not return an OpenAI-compatible model list');
  const ids = data
    .map(m => (m && typeof m === 'object' ? (m as { id?: unknown }).id : undefined))
    .filter((id): id is string => typeof id === 'string' && id.trim() !== '')
    .map(id => id.trim());

  return [...new Set(ids)].sort((a, b) => a.localeCompare(b)).slice(0, 500);
}

/**
 * Enabling is per device, not on the record: `http://127.0.0.1:11434/v1` is a
 * different program on a laptop and on a desktop, and a synced approval would
 * send the conversation to whatever answers there on the second machine.
 */
const ENABLED_KEY = 'at.local-providers.enabled';

function readEnabled(storage: Storage | undefined): string[] {
  if (!storage) return [];
  try {
    const parsed = JSON.parse(storage.getItem(ENABLED_KEY) || '[]');

    return Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === 'string') : [];
  } catch {
    return [];
  }
}

function writeEnabled(storage: Storage | undefined, ids: string[]): void {
  if (!storage) return;
  try {
    storage.setItem(ENABLED_KEY, JSON.stringify(ids));
  } catch {
    // Not persisted: the person is asked again next time, the safe direction.
  }
}

export function localProviderEnabled(id: string, storage: Storage | undefined): boolean {
  return readEnabled(storage).includes(id);
}

export function enableLocalProvider(id: string, storage: Storage | undefined): void {
  const ids = readEnabled(storage);
  if (!ids.includes(id)) writeEnabled(storage, [...ids, id]);
}

export function disableLocalProvider(id: string, storage: Storage | undefined): void {
  writeEnabled(storage, readEnabled(storage).filter(v => v !== id));
}
