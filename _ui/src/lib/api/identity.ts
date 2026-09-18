import axios from 'axios';
import type { LoginResult } from './auth';
export const identityAPI = axios.create({ baseURL: 'auth' });
export interface AuthSettings {
  version: number; origin: string; allowed_origins?: string[]; session_ttl_seconds: number; remember_ttl_seconds: number;
  local_login_enabled: boolean; local_login_collapsed: boolean; passkey_login_disabled: boolean; signup_admission: 'invite_only' | 'approval_required';
  display_title: string; mfa_policy: 'enrolled_required'; max_sessions: number;
}
/**
 * A plain OAuth2 authorization-code client. There is no protocol selector and
 * no issuer URL: every endpoint is entered explicitly, so the stored
 * configuration is the configuration, with no discovery document deciding it
 * at sign-in time. `userinfo_url` and `jwks_url` are the two claim sources and
 * at least one is required.
 */
export interface IdentityProvider {
  id: string; label: string; enabled: boolean; version: number;
  client_id: string; has_client_secret?: boolean; auth_url: string;
  token_url: string; userinfo_url: string; jwks_url: string; scopes: string[];
  subject_claim: string; auth_header_style: string; roles_claims?: string[];
}
export interface IdentityLink { id: string; provider_id: string; issuer: string; subject: string; email?: string; email_verified: boolean }
export interface RecentProof { proof: string; expires_in: number }
export const verifyMFA = async (challenge: string, code: string) => (await identityAPI.post<LoginResult>('mfa/verify', { challenge, code })).data;
export function validateSettings(s: AuthSettings): string {
  if (!Number.isInteger(s.version) || s.version < 1) return 'Reload settings before saving.';
  const origins = [s.origin, ...(s.allowed_origins || [])];
  if (origins.length > 17) return 'Use at most 16 additional sign-in addresses.';
  if (new Set(origins).size !== origins.length) return 'Each sign-in address must be unique.';
  for (const origin of origins) {
    try {
      const url = new URL(origin);
      const loopback = url.hostname === 'localhost' || url.hostname === '[::1]' || /^127\.(\d{1,3}\.){2}\d{1,3}$/.test(url.hostname);
      if (origin !== url.origin || url.hostname.includes('*') || (url.protocol !== 'https:' && !(url.protocol === 'http:' && loopback))) return 'Use a full HTTPS origin without a path, wildcard or trailing slash. HTTP is allowed only on loopback.';
    } catch { return 'Enter a valid sign-in address, for example https://at.example.com.'; }
  }
  if (!Number.isInteger(s.session_ttl_seconds) || s.session_ttl_seconds < 600 || s.session_ttl_seconds > 86400) return 'Session lifetime must be between 10m and 1d.';
  if (!Number.isInteger(s.remember_ttl_seconds) || s.remember_ttl_seconds < s.session_ttl_seconds || s.remember_ttl_seconds > 2592000) return 'Remembered lifetime must be at least the session lifetime and at most 30d (4w2d).';
  if (!['invite_only', 'approval_required'].includes(s.signup_admission) || s.mfa_policy !== 'enrolled_required' || s.max_sessions !== 20) return 'Invalid admission or security policy. Reload settings.';
  return '';
}

const durationUnits: Record<string, number> = { w: 604800, d: 86400, h: 3600, m: 60, s: 1 };
export function parseAuthDuration(value: string): number {
  const text = value.trim();
  if (!/^(?:\d+(?:\.\d+)?[wdhms])+$/.test(text)) return NaN;
  let seconds = 0;
  for (const part of text.matchAll(/(\d+(?:\.\d+)?)([wdhms])/g)) seconds += Number(part[1]) * durationUnits[part[2]];
  return Number.isSafeInteger(seconds) && seconds > 0 ? seconds : NaN;
}

export function formatAuthDuration(seconds: number): string {
  let text = '';
  for (const [unit, size] of Object.entries(durationUnits)) {
    const count = Math.floor(seconds / size);
    if (count) { text += `${count}${unit}`; seconds %= size; }
  }
  return text || '0s';
}
