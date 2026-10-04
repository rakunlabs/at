import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

/**
 * An OpenAI-compatible endpoint the browser calls directly from Chats.
 *
 * The server stores and validates these records and never sends a request to
 * one, which is what lets a loopback base URL mean the user's own machine.
 */
export interface LocalChatProvider {
  id: string;
  /** Lowercase; part of the model reference `local:<name>/<model>`. */
  name: string;
  /** API root, e.g. http://127.0.0.1:11434/v1. */
  base_url: string;
  /** '***' on ordinary reads; reveal returns the stored value. */
  api_key?: string;
  headers?: Record<string, string>;
  created_at?: string;
  updated_at?: string;
}

export interface LocalChatProviderSecrets {
  api_key: string;
  headers: Record<string, string>;
}

export async function listLocalChatProviders(): Promise<LocalChatProvider[]> {
  const res = await api.get<{ providers: LocalChatProvider[] | null }>('/chats/local-providers');

  return res.data?.providers ?? [];
}

export async function saveLocalChatProviders(providers: LocalChatProvider[]): Promise<LocalChatProvider[]> {
  const res = await api.put<{ providers: LocalChatProvider[] | null }>('/chats/local-providers', { providers });

  return res.data?.providers ?? [];
}

/** Credentials for one record, fetched when the browser is about to call it. */
export async function revealLocalChatProvider(id: string): Promise<LocalChatProviderSecrets> {
  const res = await api.post<Partial<LocalChatProviderSecrets>>(
    `/chats/local-providers/${encodeURIComponent(id)}/reveal`,
  );

  return { api_key: res.data?.api_key ?? '', headers: res.data?.headers ?? {} };
}

export interface LocalGenerationObservation {
  trace_id: string;
  session_id?: string;
  provider: string;
  model: string;
  status: 'ok' | 'error';
  latency_ms: number;
  finish_reason?: string;
  error?: string;
  input_tokens: number;
  output_tokens: number;
  input?: string;
  output?: string;
}

/**
 * Reports one local-provider generation to Traces. Never throws: a trace is
 * bookkeeping and must not fail the turn.
 */
export async function reportLocalGeneration(obs: LocalGenerationObservation): Promise<void> {
  try {
    await api.post('/chats/generation-observations', obs);
  } catch {
    /* ignored by design */
  }
}
