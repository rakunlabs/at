import axios from 'axios';
import type { RawCredential } from '../helper/webauthn';

// Relative to the SPA document so deployments under server.base_path stay same-origin.
const api = axios.create({ baseURL: 'auth' });

export interface AuthIdentity {
  subject: string;
  name: string;
  provider: string;
  roles?: string[];
  expires_at?: string;
  claims?: {
    session_id?: string;
    session_expires_at?: string;
    remember_me?: boolean;
  };
}

export interface AuthStatus {
  enabled: boolean;
  passkeys: boolean;
  setup_required: boolean;
  local_login: boolean;
  display_title: string;
}

export interface AuthPasskey {
  id: string;
  name: string;
  created_at: string;
  last_used_at: string | null;
}

export interface MobileAuthRequest {
  request_id: string;
  device_name: string;
  remember_me: boolean;
  created_at: string;
  expires_at: string;
  issuer: string;
  callback_uri: string;
}

// MobilePKCEV1 uses canonical unpadded base64url for exactly 32 random bytes.
export function validMobileSecret(value: unknown): value is string {
  return typeof value === 'string' && /^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/.test(value);
}

export function mobileRequestID(query: string): string | null {
  const params = new URLSearchParams(query);
  const id = params.get('request_id');
  return params.getAll('request_id').length === 1 && validMobileSecret(id) ? id : null;
}

export function validateMobileCallback(value: unknown, approve: boolean): string {
  const invalid = () => new Error('Invalid mobile callback');
  if (typeof value !== 'string' || !value.startsWith('atmobile://auth/callback?') || /[\s#]/.test(value)) throw invalid();
  const url = new URL(value);
  if (url.protocol !== 'atmobile:' || url.hostname !== 'auth' || url.pathname !== '/callback' || url.username || url.password || url.port || url.hash) throw invalid();
  const params = url.searchParams;
  const result = approve ? 'code' : 'error';
  if (Array.from(params.keys()).length !== 2 || params.getAll('state').length !== 1 || params.getAll(result).length !== 1 || !validMobileSecret(params.get('state'))) throw invalid();
  if (approve ? !validMobileSecret(params.get('code')) : params.get('error') !== 'access_denied') throw invalid();
  return value;
}

export async function getMobileAuthRequest(id: string, signal: AbortSignal): Promise<MobileAuthRequest> {
  return (await api.get<MobileAuthRequest>(`mobile/requests/${encodeURIComponent(id)}`, { signal, headers: { 'Cache-Control': 'no-cache' } })).data;
}

export async function decideMobileAuthRequest(id: string, approve: boolean, signal: AbortSignal): Promise<string> {
  const { data } = await api.post(`mobile/${approve ? 'approve' : 'deny'}`, { request_id: id }, { signal });
  return validateMobileCallback(data?.redirect_url, approve);
}

export async function getAuthStatus(): Promise<AuthStatus> {
  const { data } = await api.get<AuthStatus>('status', { headers: { 'Cache-Control': 'no-cache' } });
  if (data?.enabled !== true && data?.enabled !== false) throw new Error('Invalid authentication status');
  return { ...data, passkeys: data.enabled === true && data.passkeys === true };
}

export interface MFAChallenge { mfa_required: true; challenge: string; expires_in: number; methods: string[] }
export type LoginResult = AuthIdentity | MFAChallenge;
export async function loginWithPassword(username: string, password: string, remember_me = false, signal?: AbortSignal): Promise<LoginResult> {
  return (await api.post<LoginResult>('login', { username, password, remember_me }, { signal })).data;
}

export async function listAuthPasskeys(signal?: AbortSignal): Promise<{ items: AuthPasskey[] }> {
  return (await api.get<{ items: AuthPasskey[] }>('passkeys', { signal })).data;
}

export async function beginPasskeyEnrollment(name: string, current_password: string, signal: AbortSignal): Promise<{ publicKey: PublicKeyCredentialCreationOptionsJSON }> {
  return (await api.post('passkeys/enroll/begin', { name, current_password }, { signal })).data;
}

export async function finishPasskeyEnrollment(credential: RawCredential, signal: AbortSignal): Promise<void> {
  await api.post('passkeys/enroll/finish', credential, { signal });
}

export async function beginPasskeyLogin(username: string, remember_me: boolean, signal: AbortSignal): Promise<{ publicKey: PublicKeyCredentialRequestOptionsJSON }> {
  return (await api.post('passkeys/login/begin', { username, remember_me }, { signal })).data;
}

export async function finishPasskeyLogin(credential: RawCredential, signal: AbortSignal): Promise<LoginResult> {
  return (await api.post<LoginResult>('passkeys/login/finish', credential, { signal })).data;
}

export async function deleteAuthPasskey(id: string, current_password: string, signal: AbortSignal): Promise<void> {
  await api.post(`passkeys/${encodeURIComponent(id)}/delete`, { current_password }, { signal });
}

export interface AuthUser {
  id: string;
  username: string;
  admin: boolean;
  disabled: boolean;
}

export interface AuthUserPage {
  data: AuthUser[];
  next_cursor: string;
}

export interface CreateAuthUser {
  username: string;
  password: string;
  admin: boolean;
}

export async function getAuthIdentity(): Promise<AuthIdentity> {
  return (await api.get<AuthIdentity>('me', { headers: { 'Cache-Control': 'no-cache' } })).data;
}

export async function logoutAuth(): Promise<void> {
  await api.post('logout', {});
}

export async function listAuthUsers(after = ''): Promise<AuthUserPage> {
  return (await api.get<AuthUserPage>('users', { params: { limit: 50, after } })).data;
}

export async function createAuthUser(user: CreateAuthUser): Promise<AuthIdentity> {
  return (await api.post<AuthIdentity>('users', user)).data;
}

export async function setAuthUserEnabled(id: string, enabled: boolean): Promise<void> {
  await api.post(`users/${encodeURIComponent(id)}/${enabled ? 'enable' : 'disable'}`);
}

export async function revokeAuthUserSessions(id: string): Promise<void> {
  await api.post(`users/${encodeURIComponent(id)}/revoke-sessions`);
}

export async function resetAuthUserPassword(id: string, password: string): Promise<void> {
  await api.post(`users/${encodeURIComponent(id)}/password`, { password });
}

export async function changeAuthPassword(current_password: string, new_password: string): Promise<void> {
  await api.post('password', { current_password, new_password });
}

export function authErrorMessage(error: unknown, fallback: string): string {
  return axios.isAxiosError<{ message?: string }>(error) ? error.response?.data?.message || fallback : fallback;
}

export function isAuthUnauthorized(error: unknown): boolean {
  return axios.isAxiosError(error) && error.response?.status === 401;
}

// An unclaimed installation refuses management with 403 and a fixed message.
// Detect it so a stale or cached client still reaches the setup screen.
export function isSetupRequired(error: unknown): boolean {
  // Compared by name, not by class: this module is also loaded standalone,
  // so it must not take a runtime dependency on the transport.
  if (error instanceof Error && error.name === 'InstallationSetupRequired') return true;
  if (!axios.isAxiosError(error) || error.response?.status !== 403) return false;
  const data = error.response?.data as { message?: string } | undefined;
  return data?.message === 'installation setup required';
}

// The byte ceiling is an implementation bound, not guidance, so it is reported
// only when a password actually exceeds it.
export function passwordPolicyError(password: string): string {
  if (new TextEncoder().encode(password).length > 1024) return 'Use at most 1024 UTF-8 bytes.';
  return Array.from(password).length < 8 ? 'Use at least 8 characters.' : '';
}
