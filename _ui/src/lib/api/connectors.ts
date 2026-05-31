import axios from 'axios';

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

export type ConnectorAuthKind = 'oauth2' | 'token' | 'custom';
export type ConnectorFieldType = 'text' | 'secret';

/** A single credential input a connector needs. The `key` is the full variable
 *  name a skill will read (e.g. "spotify_client_id"). */
export interface ConnectorField {
  key: string;
  label?: string;
  type?: ConnectorFieldType;
  required?: boolean;
  placeholder?: string;
  help?: string;
}

export interface ConnectorOAuth {
  auth_url: string;
  token_url: string;
  scopes?: string[];
  access_type?: string;
  prompt?: string;
  use_pkce?: boolean;
  userinfo_url?: string;
  account_label_path?: string;
  extra_auth_params?: Record<string, string>;
}

/** A data-driven definition of an external-service connection TYPE. */
export interface Connector {
  slug: string;
  name: string;
  description?: string;
  icon?: string;
  auth_kind: ConnectorAuthKind;
  oauth?: ConnectorOAuth;
  fields?: ConnectorField[];
  builtin?: boolean;
  created_at?: string;
  updated_at?: string;
}

export interface ConnectorInput {
  slug: string;
  name: string;
  description?: string;
  icon?: string;
  auth_kind: ConnectorAuthKind;
  oauth?: ConnectorOAuth;
  fields?: ConnectorField[];
}

// ─── CRUD ───

export async function listConnectors(): Promise<Connector[]> {
  const res = await api.get<Connector[]>('/connectors');
  return res.data ?? [];
}

export async function getConnector(slug: string): Promise<Connector> {
  const res = await api.get<Connector>(`/connectors/${encodeURIComponent(slug)}`);
  return res.data;
}

export async function createConnector(input: ConnectorInput): Promise<Connector> {
  const res = await api.post<Connector>('/connectors', input);
  return res.data;
}

export async function updateConnector(slug: string, input: ConnectorInput): Promise<Connector> {
  const res = await api.put<Connector>(`/connectors/${encodeURIComponent(slug)}`, input);
  return res.data;
}

export async function deleteConnector(slug: string): Promise<void> {
  await api.delete(`/connectors/${encodeURIComponent(slug)}`);
}
