import axios from 'axios';

// This module is exercised by `tests/media.test.mjs`, whose harness compiles
// exactly one `.ts` file to a data URL. Runtime relative imports would not
// resolve there, so every type and helper below is declared locally.

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

/** `''` disables media storage entirely. */
export type MediaBackend = '' | 'filesystem' | 's3';

export interface MediaFilesystemSettings {
  /** Absolute path, writable by the service user. */
  root: string;
}

export interface MediaS3Settings {
  endpoint: string;
  region: string;
  bucket: string;
  prefix: string;
  access_key_id: string;
  /** Always `''` on read. Sending `''` keeps the stored secret. */
  secret_access_key: string;
  use_path_style: boolean;
}

export interface MediaSettings {
  version: number;
  backend: MediaBackend;
  filesystem: MediaFilesystemSettings;
  s3: MediaS3Settings;
  /**
   * Read-only signal that a secret is stored. The server never returns the
   * secret itself, and accepts-and-ignores this key on write — so the request
   * body is rebuilt without it.
   */
  secret_access_key_set?: boolean;
}

/** The exact document `PUT` / `POST …/test` accept. Unknown keys are a 400. */
export interface MediaSettingsBody {
  version: number;
  backend: MediaBackend;
  filesystem: MediaFilesystemSettings;
  s3: MediaS3Settings;
}

/** One stored image. `storage_key` is backend-internal, never a URL. */
export interface MediaObject {
  id: string;
  owner_user_id: string;
  backend: string;
  storage_key: string;
  content_type: string;
  size_bytes: number;
  checksum: string;
  created_at: string;
}

export interface MediaTestResult {
  ok: boolean;
  /** Verbatim upstream failure. Present on a 502 probe. */
  message?: string;
}

// ─── Limits ───

/** Server-side upload ceiling. Anything larger is a 413. */
export const MEDIA_MAX_UPLOAD_BYTES = 16 * 1024 * 1024;

/** The content types the server sniffs for. A mismatch is a 415. */
export const MEDIA_ALLOWED_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'] as const;

export const MEDIA_ALLOWED_LABEL = 'PNG, JPEG, GIF or WebP';

// ─── Contract guards ───

const EMPTY_S3: MediaS3Settings = {
  endpoint: '', region: '', bucket: '', prefix: '',
  access_key_id: '', secret_access_key: '', use_path_style: false,
};

const text = (value: unknown): string => (typeof value === 'string' ? value : '');

/**
 * Rebuild the settings document from an allowlist. `secret_access_key_set` is a
 * read-only signal: forwarding it would be inventing a field, and every other
 * unknown key is a hard 400. An empty `secret_access_key` is kept as `''`
 * rather than dropped, because `''` is the wire signal for "keep the stored
 * secret" — omitting it would change meaning.
 */
export function mediaSettingsBody(settings: MediaSettings): MediaSettingsBody {
  const s3 = settings?.s3 ?? EMPTY_S3;
  return {
    version: typeof settings?.version === 'number' ? settings.version : 0,
    backend: (settings?.backend ?? '') as MediaBackend,
    filesystem: { root: text(settings?.filesystem?.root) },
    s3: {
      endpoint: text(s3.endpoint),
      region: text(s3.region),
      bucket: text(s3.bucket),
      prefix: text(s3.prefix),
      access_key_id: text(s3.access_key_id),
      secret_access_key: text(s3.secret_access_key),
      use_path_style: s3.use_path_style === true,
    },
  };
}

/** A blank settings document, for a first-time page load that 404s or fails. */
export function emptyMediaSettings(): MediaSettings {
  return { version: 0, backend: '', filesystem: { root: '' }, s3: { ...EMPTY_S3 }, secret_access_key_set: false };
}

// ─── Settings ───

export async function getMediaSettings(): Promise<MediaSettings> {
  const res = await api.get<MediaSettings>('/media/settings');
  return res.data;
}

/** Returns the redacted settings with `version` incremented. 409 means stale. */
export async function putMediaSettings(settings: MediaSettings): Promise<MediaSettings> {
  const res = await api.put<MediaSettings>('/media/settings', mediaSettingsBody(settings));
  return res.data;
}

/**
 * Probe the *submitted* settings without saving, so an operator can verify
 * credentials before the first write. A reachable backend answers
 * `{ok:true}`; a 502 carries the verbatim upstream error.
 */
export async function testMediaSettings(settings: MediaSettings): Promise<MediaTestResult> {
  const res = await api.post<MediaTestResult>('/media/settings/test', mediaSettingsBody(settings));
  return res.data;
}

// ─── Objects ───

const mediaPath = (id: string) => `/media/${encodeURIComponent(id)}`;

/**
 * Same-origin, cookie-authenticated URL for an `<img src>`. Relative, so it
 * resolves against the SPA base path exactly like the axios `baseURL` does.
 */
export const mediaImageURL = (id: string) => `api/v1/media/${encodeURIComponent(id)}`;

/** Multipart upload. The server sniffs the type and ignores our Content-Type. */
export async function uploadMedia(file: Blob, name = 'image'): Promise<MediaObject> {
  const form = new FormData();
  form.append('file', file, name);
  const res = await api.post<MediaObject>('/media', form);
  return res.data;
}

/** Raw bytes, owner-scoped. 404 for an unknown *or foreign* id. */
export async function getMediaBlob(id: string): Promise<Blob> {
  const res = await api.get<Blob>(mediaPath(id), { responseType: 'blob' });
  return res.data;
}

export async function deleteMedia(id: string): Promise<void> {
  await api.delete(mediaPath(id));
}

// ─── Data-URI bridge ───

/** `data:image/png;base64,AAAA` → a `Blob`. Throws on a non-base64 data URI. */
export function dataUrlToBlob(dataUrl: string): Blob {
  const comma = dataUrl.indexOf(',');
  const header = comma < 0 ? '' : dataUrl.slice(5, comma);
  if (comma < 0 || !/;base64$/i.test(header)) throw new Error('not a base64 data URI');
  const type = header.slice(0, header.length - ';base64'.length) || 'application/octet-stream';
  const binary = atob(dataUrl.slice(comma + 1));
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i);
  return new Blob([bytes], { type });
}

function blobToDataUrl(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ''));
    reader.onerror = () => reject(new Error('failed to read media'));
    reader.readAsDataURL(blob);
  });
}

/**
 * Re-inline a stored image. Providers need image content in the request body,
 * while a restored transcript only carries an id — so the bytes are fetched
 * through the authenticated same-origin request and re-encoded. Callers should
 * cache the result per id: media objects are immutable.
 */
export async function getMediaDataURL(id: string): Promise<string> {
  return blobToDataUrl(await getMediaBlob(id));
}

// ─── Errors ───

export function mediaErrorStatus(error: unknown): number {
  const status = (error as any)?.response?.status;
  return typeof status === 'number' ? status : 0;
}

/** Backend errors are `{"message":"…"}`; anything else falls back. */
export function mediaErrorMessage(error: unknown, fallback: string): string {
  const message = (error as any)?.response?.data?.message;
  return typeof message === 'string' && message ? message : fallback;
}

/** A 503 means media storage is disabled, not that the request was wrong. */
export const isMediaStorageDisabled = (error: unknown) => mediaErrorStatus(error) === 503;

/** A 409 means someone else saved first; the local version is stale. */
export const isMediaSettingsConflict = (error: unknown) => mediaErrorStatus(error) === 409;

/**
 * Per-image upload failure text. 413 and 415 are precise, actionable limits and
 * deserve to say so instead of collapsing into "upload failed".
 */
export function mediaUploadErrorMessage(error: unknown, name: string): string {
  const label = name || 'image';
  switch (mediaErrorStatus(error)) {
    case 413:
      return `"${label}" is larger than 16 MB and was not saved to history`;
    case 415:
      return `"${label}" is not a supported image type (${MEDIA_ALLOWED_LABEL}) and was not saved to history`;
    case 503:
      return 'Media storage is not configured, so images are not saved to history';
    case 401:
    case 403:
      return `You are not allowed to store "${label}"`;
    default:
      return mediaErrorMessage(error, `Could not save "${label}" to history`);
  }
}

/** Settings save/probe failure text, keyed on the codes the endpoint returns. */
export function mediaSettingsErrorMessage(error: unknown): string {
  switch (mediaErrorStatus(error)) {
    case 409:
      return 'These settings were changed elsewhere. Reload the saved settings before saving again.';
    case 413:
      return 'The settings document is too large. Shorten the path, prefix or endpoint values.';
    case 503:
      return mediaErrorMessage(error, 'Media storage is unavailable right now. Retry when the server is ready.');
    default:
      return mediaErrorMessage(error, 'Could not save media storage settings.');
  }
}
