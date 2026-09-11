import axios from 'axios';
import type { LoginResult } from './auth';
export const identityAPI = axios.create({ baseURL: 'auth' });
export interface AuthSettings {
  version: number; origin: string; session_ttl_seconds: number; remember_ttl_seconds: number;
  local_login_enabled: boolean; signup_admission: 'invite_only' | 'approval_required';
  display_title: string; mfa_policy: 'enrolled_required'; max_sessions: number;
}
export interface IdentityProvider {
  id: string; label: string; mode: 'oidc' | 'oauth2'; enabled: boolean; version: number;
  issuer: string; client_id: string; has_client_secret?: boolean; auth_url: string;
  token_url: string; userinfo_url: string; jwks_url: string; scopes: string[];
  subject_claim: string; auth_header_style: string;
}
export interface IdentityLink { id: string; provider_id: string; issuer: string; subject: string; email?: string; email_verified: boolean }
export interface RecentProof { proof: string; expires_in: number }
export const verifyMFA = async (challenge: string, code: string) => (await identityAPI.post<LoginResult>('mfa/verify', { challenge, code })).data;
export function validateSettings(s: AuthSettings): string {
  if (!Number.isInteger(s.version) || s.version < 1) return 'Reload settings before saving.';
  if (!Number.isInteger(s.session_ttl_seconds) || s.session_ttl_seconds < 600 || s.session_ttl_seconds > 86400) return 'Session lifetime must be 600–86400 seconds.';
  if (!Number.isInteger(s.remember_ttl_seconds) || s.remember_ttl_seconds < s.session_ttl_seconds || s.remember_ttl_seconds > 2592000) return 'Remembered lifetime must be at least the session lifetime and at most 2592000 seconds.';
  if (!['invite_only', 'approval_required'].includes(s.signup_admission) || s.mfa_policy !== 'enrolled_required' || s.max_sessions !== 20) return 'Invalid admission or security policy. Reload settings.';
  return '';
}
