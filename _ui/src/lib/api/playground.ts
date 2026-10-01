import axios from 'axios';

// This module is exercised by `tests/playground.test.mjs`, whose harness
// compiles exactly one `.ts` file to a data URL. Runtime relative imports would
// not resolve there, so every type and helper below is declared locally.

const api = axios.create({
  baseURL: 'api/v1',
});

// ─── Types ───

/** A durable playground transcript. `config` is opaque client workbench state. */
export interface PlaygroundConversation {
  id: string;
  owner_user_id: string;
  title: string;
  system_prompt: string;
  provider_key: string;
  model: string;
  config: Record<string, unknown>;
  /** Absent when the conversation was started from scratch or the parent was deleted. */
  forked_from_id?: string;
  /** Absent when the conversation was started from scratch. */
  forked_from_sequence?: number;
  imported_from_share_id?: string;
  imported_from_share_version?: number;
  created_at: string;
  updated_at: string;
}

export type PlaygroundRole = 'user' | 'assistant' | 'tool';

/** One transcript entry. `sequence` is assigned by the store, starting at 1. */
export interface PlaygroundMessage {
  id: string;
  conversation_id: string;
  sequence: number;
  role: PlaygroundRole;
  provider_key: string;
  model: string;
  /** Opaque OpenAI-shaped message body. */
  data: Record<string, unknown>;
  created_at: string;
}

/** Keyset cursor meta. `next_before` is only present when a full page came back. */
export interface PlaygroundListMeta {
  limit?: number;
  next_before?: string;
}

export interface PlaygroundList<T> {
  data: T[];
  meta: PlaygroundListMeta;
}

/** Cursor page request. `before` is a conversation id / message id. */
export interface PlaygroundPageParams {
  limit?: number;
  before?: string;
}

/** The five patchable conversation columns. `PATCH {}` is a 400, never send it. */
export interface PlaygroundConversationInput {
  title?: string;
  system_prompt?: string;
  provider_key?: string;
  model?: string;
  config?: Record<string, unknown>;
}

export interface PlaygroundMessageInput {
  client_id?: string;
  role: PlaygroundRole;
  provider_key: string;
  model: string;
  data: Record<string, unknown>;
}

export interface ChatShareOptions {
  include_system_prompt: boolean;
  include_tool_outputs: boolean;
  include_attachments: boolean;
}

export interface ChatSharePayload {
  title: string;
  system_prompt?: string;
  provider_key?: string;
  model?: string;
  messages: PlaygroundMessage[];
}

export interface ChatShare {
  id: string;
  workspace_id: string;
  source_conversation_id?: string;
  through_sequence: number;
  version: number;
  revoked_at?: string;
  created_at: string;
  updated_at: string;
  payload: ChatSharePayload;
  options: ChatShareOptions;
}

export interface ChatSharePreview {
  payload: ChatSharePayload;
  attachment_count: number;
  options: ChatShareOptions;
}

// ─── Contract guards ───

/**
 * Every playground request body is decoded with `DisallowUnknownFields`, so an
 * unknown key is a 400. Bodies are therefore rebuilt from this allowlist rather
 * than forwarded, and `undefined` values are dropped so a caller can pass a
 * partially populated object without widening the wire shape.
 */
const CONVERSATION_FIELDS = ['title', 'system_prompt', 'provider_key', 'model', 'config'] as const;

/** Build a body carrying only defined, allowlisted keys. `null` when empty. */
export function playgroundConversationBody(input: PlaygroundConversationInput): Record<string, unknown> | null {
  const body: Record<string, unknown> = {};
  for (const field of CONVERSATION_FIELDS) {
    const value = input[field];
    if (value !== undefined) body[field] = value;
  }
  return Object.keys(body).length > 0 ? body : null;
}

const pagePath = (path: string, params?: PlaygroundPageParams) => {
  const query = new URLSearchParams();
  if (params?.limit !== undefined) query.set('limit', String(params.limit));
  if (params?.before) query.set('before', params.before);
  const suffix = query.toString();
  return suffix ? `${path}?${suffix}` : path;
};

const conversationPath = (id: string) => `/chats/conversations/${encodeURIComponent(id)}`;

// ─── Conversations ───

/** Creation ordered (`created_at DESC`). `before` pages towards older entries. */
export async function listPlaygroundConversations(params?: PlaygroundPageParams): Promise<PlaygroundList<PlaygroundConversation>> {
  const res = await api.get<PlaygroundList<PlaygroundConversation>>(pagePath('/chats/conversations', params));
  return res.data;
}

/** Keep sidebar order stable when a fetched or updated row is merged locally. */
export function sortPlaygroundConversations(items: PlaygroundConversation[]): PlaygroundConversation[] {
  return [...items].sort((a, b) => b.created_at.localeCompare(a.created_at) || b.id.localeCompare(a.id));
}

export async function createPlaygroundConversation(input: PlaygroundConversationInput = {}): Promise<PlaygroundConversation> {
  const res = await api.post<PlaygroundConversation>('/chats/conversations', playgroundConversationBody(input) ?? {});
  return res.data;
}

export async function getPlaygroundConversation(id: string): Promise<PlaygroundConversation> {
  const res = await api.get<PlaygroundConversation>(conversationPath(id));
  return res.data;
}

/** Returns `null` without calling the API when nothing changed — `PATCH {}` is a 400. */
export async function patchPlaygroundConversation(id: string, patch: PlaygroundConversationInput): Promise<PlaygroundConversation | null> {
  const body = playgroundConversationBody(patch);
  if (!body) return null;
  const res = await api.patch<PlaygroundConversation>(conversationPath(id), body);
  return res.data;
}

export async function deletePlaygroundConversation(id: string): Promise<void> {
  await api.delete(conversationPath(id));
}

/** Copies messages 1..from_sequence into a new conversation and returns the fork. */
export async function forkPlaygroundConversation(id: string, fromSequence: number, title?: string): Promise<PlaygroundConversation> {
  const body: Record<string, unknown> = { from_sequence: fromSequence };
  if (title !== undefined) body.title = title;
  const res = await api.post<PlaygroundConversation>(`${conversationPath(id)}/fork`, body);
  return res.data;
}

const workspaceHeaders = (workspaceID?: string) => workspaceID ? { headers: { 'X-AT-Workspace-ID': workspaceID } } : undefined;

export async function previewChatShare(id: string, throughSequence: number, options: ChatShareOptions, workspaceID?: string): Promise<ChatSharePreview> {
  const res = await api.post<ChatSharePreview>(`${conversationPath(id)}/shares/preview`, { through_sequence: throughSequence, options }, workspaceHeaders(workspaceID));
  return res.data;
}

export async function publishChatShare(id: string, throughSequence: number, options: ChatShareOptions, workspaceID?: string): Promise<ChatShare> {
  const res = await api.post<ChatShare>(`${conversationPath(id)}/shares`, { through_sequence: throughSequence, options }, workspaceHeaders(workspaceID));
  return res.data;
}

export async function getConversationShare(id: string, workspaceID?: string): Promise<ChatShare | null> {
  try {
    const res = await api.get<ChatShare>(`${conversationPath(id)}/share`, workspaceHeaders(workspaceID));
    return res.data;
  } catch (error: any) {
    if (error?.response?.status === 404) return null;
    throw error;
  }
}

export async function getChatShare(id: string, workspaceID?: string): Promise<ChatShare> {
  const res = await api.get<ChatShare>(`/chats/shares/${encodeURIComponent(id)}`, workspaceHeaders(workspaceID));
  return res.data;
}

export async function updateChatShare(id: string, throughSequence: number, options: ChatShareOptions, workspaceID?: string): Promise<ChatShare> {
  const res = await api.put<ChatShare>(`/chats/shares/${encodeURIComponent(id)}`, { through_sequence: throughSequence, options }, workspaceHeaders(workspaceID));
  return res.data;
}

export async function revokeChatShare(id: string, workspaceID?: string): Promise<void> {
  await api.delete(`/chats/shares/${encodeURIComponent(id)}`, workspaceHeaders(workspaceID));
}

export async function importChatShare(id: string, version: number, input: { title?: string; provider_key?: string; model?: string } = {}, workspaceID?: string): Promise<PlaygroundConversation> {
  const res = await api.post<PlaygroundConversation>(`/chats/shares/${encodeURIComponent(id)}/import`, { version, ...input }, workspaceHeaders(workspaceID));
  return res.data;
}

export function chatShareRoute(id: string): string {
  return `/chats/shared/${encodeURIComponent(id)}`;
}

export function chatShareMediaURL(shareID: string, mediaID: string, workspaceID = ''): string {
  const query = workspaceID ? `?workspace_id=${encodeURIComponent(workspaceID)}` : '';
  return `api/v1/chats/shares/${encodeURIComponent(shareID)}/media/${encodeURIComponent(mediaID)}${query}`;
}

// ─── Per-account defaults ───

/**
 * The caller's saved starting point for a NEW conversation. Existing
 * conversations keep their own persisted config; this only seeds the next one,
 * so a tuned workbench survives "New chat" instead of resetting to the
 * alphabetically first model. Owner is the authenticated account server-side —
 * there is no user id on the wire.
 */
export interface PlaygroundDefaults {
  model?: string;
  reasoning_effort?: string;
  system_prompt?: string;
  mcp_sets?: string[];
  skills?: string[];
  builtin_tools?: string[];
  frontend_tools?: string[];
}

/** An account that never saved a preset reads the shipped defaults, not a 404. */
export async function getPlaygroundDefaults(): Promise<PlaygroundDefaults> {
  const res = await api.get<PlaygroundDefaults>('/chats/defaults');
  return res.data || {};
}

const DEFAULTS_FIELDS = ['model', 'reasoning_effort', 'system_prompt', 'mcp_sets', 'skills', 'builtin_tools', 'frontend_tools'] as const;

/** Bodies are rebuilt from the allowlist: the endpoint rejects unknown fields. */
export async function savePlaygroundDefaults(input: PlaygroundDefaults): Promise<PlaygroundDefaults> {
  const body: Record<string, unknown> = {};
  for (const field of DEFAULTS_FIELDS) {
    const value = input[field];
    if (value !== undefined) body[field] = value;
  }
  const res = await api.put<PlaygroundDefaults>('/chats/defaults', body);
  return res.data || {};
}

// ─── Named presets ───

/**
 * A named workbench setup. Same payload as `PlaygroundDefaults` plus identity,
 * because a preset is a default with a name — the server shares one struct for
 * both so the two shapes cannot drift.
 *
 * Unlike the singleton default, which only seeds a NEW conversation, a preset
 * is applied on demand — including to the conversation already open, since
 * switching setups mid-session is the reason for having more than one.
 */
export interface ChatPreset extends PlaygroundDefaults {
  /** Server-assigned. Send `''` (or omit) to create a new entry. */
  id: string;
  name: string;
  created_at?: string;
  updated_at?: string;
  scope: 'personal' | 'workspace';
  can_edit: boolean;
  owner_user_id?: string;
}

const PRESET_FIELDS = ['id', 'name', ...DEFAULTS_FIELDS] as const;

/** Never `null`: the page iterates the list without a nil check. */
export async function listChatPresets(): Promise<ChatPreset[]> {
  const res = await api.get<{ presets: ChatPreset[] }>('/chats/presets');
  return (res.data?.presets ?? []).map(preset => ({ ...preset, scope: 'personal', can_edit: true }));
}

/**
 * Replaces the whole list. Add, rename, overwrite and delete are all this one
 * call, so the caller sends the list it wants to end up with. Identity
 * (`id`, `created_at`) is resolved server-side against what is stored.
 */
export async function saveChatPresets(presets: ChatPreset[]): Promise<ChatPreset[]> {
  const body = {
    presets: presets.map(preset => {
      const source = preset as unknown as Record<string, unknown>;
      const out: Record<string, unknown> = {};
      for (const field of PRESET_FIELDS) {
        const value = source[field];
        if (value !== undefined) out[field] = value;
      }
      return out;
    }),
  };
  const res = await api.put<{ presets: ChatPreset[] }>('/chats/presets', body);
  return (res.data?.presets ?? []).map(preset => ({ ...preset, scope: 'personal', can_edit: true }));
}

export async function listWorkspaceChatPresets(): Promise<ChatPreset[]> {
  const res = await api.get<{ presets: ChatPreset[] }>('/chats/workspace-presets');
  return (res.data?.presets ?? []).map(preset => ({ ...preset, scope: 'workspace', can_edit: !!preset.can_edit }));
}

function workspacePresetBody(preset: Pick<ChatPreset, 'name'> & PlaygroundDefaults): Record<string, unknown> {
  const source = preset as unknown as Record<string, unknown>;
  const out: Record<string, unknown> = { name: preset.name };
  for (const field of DEFAULTS_FIELDS) {
    const value = source[field];
    if (value !== undefined) out[field] = value;
  }
  return out;
}

export async function createWorkspaceChatPreset(preset: Pick<ChatPreset, 'name'> & PlaygroundDefaults): Promise<ChatPreset> {
  const res = await api.post<ChatPreset>('/chats/workspace-presets', workspacePresetBody(preset));
  return { ...res.data, scope: 'workspace', can_edit: true };
}

export async function updateWorkspaceChatPreset(id: string, preset: Pick<ChatPreset, 'name'> & PlaygroundDefaults): Promise<ChatPreset> {
  const res = await api.put<ChatPreset>(`/chats/workspace-presets/${encodeURIComponent(id)}`, workspacePresetBody(preset));
  return { ...res.data, scope: 'workspace', can_edit: true };
}

export async function deleteWorkspaceChatPreset(id: string): Promise<void> {
  await api.delete(`/chats/workspace-presets/${encodeURIComponent(id)}`);
}

// ─── Messages ───

/** Chronological ascending. `meta.next_before` is the OLDEST returned message. */
export async function listPlaygroundMessages(id: string, params?: PlaygroundPageParams): Promise<PlaygroundList<PlaygroundMessage>> {
  const res = await api.get<PlaygroundList<PlaygroundMessage>>(pagePath(`${conversationPath(id)}/messages`, params));
  return res.data;
}

/** Appends in order. The server caps a batch at 200 entries. */
export const PLAYGROUND_MESSAGE_BATCH_MAX = 200;

export async function appendPlaygroundMessages(id: string, messages: PlaygroundMessageInput[]): Promise<PlaygroundMessage[]> {
  const body = {
    messages: messages.map(m => ({ ...(m.client_id ? { client_id: m.client_id } : {}), role: m.role, provider_key: m.provider_key, model: m.model, data: m.data })),
  };
  const res = await api.post<PlaygroundList<PlaygroundMessage>>(`${conversationPath(id)}/messages`, body);
  return res.data?.data ?? [];
}

/** Drops every message with `sequence >= fromSequence`. */
export async function truncatePlaygroundMessages(id: string, fromSequence: number): Promise<void> {
  await api.delete(`${conversationPath(id)}/messages?from_sequence=${encodeURIComponent(String(fromSequence))}`);
}

// ─── Image persistence ───

/**
 * A persisted `data` payload is capped at 1 MiB and a single inline image
 * data-URI blows past that, so an image is never stored inline. When media
 * storage is configured the bytes go to `POST /media` and the transcript keeps
 * a `media_id` reference; otherwise it keeps a visible descriptor rather than
 * dropping the image silently, so the transcript stays honest about what was
 * there.
 */
export interface PlaygroundOmittedImage {
  type: 'image';
  name: string;
  bytes: number;
  omitted: true;
}

/** An image whose bytes live in media storage and can be re-inlined on demand. */
export interface PlaygroundStoredImage {
  type: 'image';
  media_id: string;
  name: string;
  bytes: number;
}

export function isOmittedImage(part: unknown): part is PlaygroundOmittedImage {
  const p = part as Record<string, unknown> | null;
  return !!p && typeof p === 'object' && p.type === 'image' && p.omitted === true;
}

export function isStoredImage(part: unknown): part is PlaygroundStoredImage {
  const p = part as Record<string, unknown> | null;
  return !!p && typeof p === 'object' && p.type === 'image'
    && typeof p.media_id === 'string' && p.media_id !== '' && p.omitted !== true;
}

/**
 * Uploads one image and resolves to its media id, or to `''` when it could not
 * be stored. Rejecting is equivalent to resolving `''` — both fall back to the
 * omitted descriptor — so a disabled or failing media backend can never break
 * persistence of the surrounding turn.
 */
export type PlaygroundImageUploader = (dataUrl: string, name: string) => Promise<string>;

const isDataUrl = (url: unknown): url is string => typeof url === 'string' && url.startsWith('data:');

/** `data:image/png;base64,AAAA` → `image.png`. */
function nameFromDataUrl(url: string, index: number): string {
  const mime = /^data:([^;,]+)/.exec(url)?.[1] ?? '';
  const subtype = mime.split('/')[1] || '';
  const ext = subtype ? subtype.split('+')[0] : 'bin';
  return `image-${index + 1}.${ext}`;
}

/** A non-image attachment (PDF, audio, video) whose bytes are in media storage. */
export interface PlaygroundStoredFile {
  type: 'file';
  media_id: string;
  name: string;
  bytes: number;
  mime_type: string;
  /** Marks a user attachment (re-sent to the model) as opposed to a delivered artifact. */
  attachment: true;
}

/** A non-image attachment that could not be stored. */
export interface PlaygroundOmittedFile {
  type: 'file';
  name: string;
  bytes: number;
  mime_type: string;
  attachment: true;
  omitted: true;
}

/**
 * The inline data-URI a content part carries, with its MIME type, or null.
 * Covers every shape the Chats composer sends: image_url, file (PDF and other
 * documents), input_audio and video_url.
 */
function inlineAttachment(p: Record<string, any>): { url: string; mime: string; image: boolean } | null {
  switch (p?.type) {
    case 'image_url': {
      const url = p.image_url?.url;
      return isDataUrl(url) ? { url, mime: /^data:([^;,]+)/.exec(url)?.[1] ?? '', image: true } : null;
    }
    case 'file': {
      const url = p.file?.file_data;
      return isDataUrl(url) ? { url, mime: /^data:([^;,]+)/.exec(url)?.[1] ?? '', image: false } : null;
    }
    case 'video_url': {
      const url = p.video_url?.url;
      return isDataUrl(url) ? { url, mime: /^data:([^;,]+)/.exec(url)?.[1] ?? '', image: false } : null;
    }
    case 'input_audio': {
      const data = p.input_audio?.data;
      if (typeof data !== 'string' || !data) return null;
      const format = String(p.input_audio?.format || 'wav');
      const mime = `audio/${format === 'mp3' ? 'mpeg' : format}`;
      return { url: `data:${mime};base64,${data}`, mime, image: false };
    }
  }
  return null;
}

function attachmentName(p: Record<string, any>, names: string[], index: number, url: string, image: boolean): string {
  const given = names[index] || (typeof p.name === 'string' && p.name) || (typeof p.file?.filename === 'string' && p.file.filename);
  if (given) return given;
  if (image) return nameFromDataUrl(url, index);
  const subtype = (/^data:[^/]+\/([^;,]+)/.exec(url)?.[1] ?? 'bin').split('+')[0];
  return `attachment-${index + 1}.${subtype}`;
}

/**
 * Return a copy of an OpenAI-shaped message body with every inline attachment
 * (image, PDF, audio, video) replaced by a descriptor. `names` supplies the
 * original file names positionally; anything missing falls back to a
 * mime-derived name. The input is never mutated, so the in-memory transcript
 * keeps its attachments.
 */
export function stripPlaygroundImages(data: Record<string, unknown>, names: string[] = []): Record<string, unknown> {
  const content = data?.content;
  if (!Array.isArray(content)) return { ...data };
  let stripped = 0;
  const parts = content.map(part => {
    const p = part as Record<string, any>;
    const inline = inlineAttachment(p);
    if (!inline) return part;
    const index = stripped++;
    const name = attachmentName(p, names, index, inline.url, inline.image);
    if (inline.image) {
      const descriptor: PlaygroundOmittedImage = { type: 'image', name, bytes: inline.url.length, omitted: true };
      return descriptor;
    }
    const descriptor: PlaygroundOmittedFile = { type: 'file', name, bytes: inline.url.length, mime_type: inline.mime, attachment: true, omitted: true };
    return descriptor;
  });
  return stripped > 0 ? { ...data, content: parts } : { ...data };
}

/**
 * Same contract as `stripPlaygroundImages`, except each attachment data-URI is
 * first offered to `upload`. A returned media id becomes a durable reference
 * (`{type:'image', media_id}` for images, `{type:'file', media_id, mime_type,
 * attachment:true}` for everything else); an empty id or a rejection falls
 * back to the omitted descriptor, so an unconfigured or failing media backend
 * degrades instead of losing the turn. The input is never mutated.
 */
export async function persistPlaygroundImages(
  data: Record<string, unknown>,
  names: string[] = [],
  upload?: PlaygroundImageUploader,
): Promise<Record<string, unknown>> {
  const content = data?.content;
  if (!Array.isArray(content) || !upload) return stripPlaygroundImages(data, names);

  let found = 0;
  const parts: unknown[] = [];
  for (const part of content) {
    const p = part as Record<string, any>;
    const inline = inlineAttachment(p);
    if (!inline) {
      parts.push(part);
      continue;
    }
    const index = found++;
    const name = attachmentName(p, names, index, inline.url, inline.image);
    let mediaID = '';
    try {
      mediaID = (await upload(inline.url, name)) || '';
    } catch {
      // The uploader owns reporting; an unstored attachment still persists as a descriptor.
      mediaID = '';
    }
    const bytes = inline.url.length;
    if (inline.image) {
      const stored: PlaygroundStoredImage = { type: 'image', media_id: mediaID, name, bytes };
      const omitted: PlaygroundOmittedImage = { type: 'image', name, bytes, omitted: true };
      parts.push(mediaID ? stored : omitted);
    } else {
      const stored: PlaygroundStoredFile = { type: 'file', media_id: mediaID, name, bytes, mime_type: inline.mime, attachment: true };
      const omitted: PlaygroundOmittedFile = { type: 'file', name, bytes, mime_type: inline.mime, attachment: true, omitted: true };
      parts.push(mediaID ? stored : omitted);
    }
  }
  return found > 0 ? { ...data, content: parts } : { ...data };
}

// ─── Errors ───

/** Backend errors are `{"message":"…"}`; anything else falls back. */
export function playgroundErrorMessage(error: unknown, fallback: string): string {
  const message = (error as any)?.response?.data?.message;
  return typeof message === 'string' && message ? message : fallback;
}

export function playgroundErrorStatus(error: unknown): number {
  const status = (error as any)?.response?.status;
  return typeof status === 'number' ? status : 0;
}

// ─── Presentation helpers ───

export const playgroundRoute = (id: string) => `/chats/${encodeURIComponent(id)}`;

/** Derive a conversation title from the first user message. */
export function playgroundTitleFrom(text: string, max = 60): string {
  const flat = (text || '').replace(/\s+/g, ' ').trim();
  if (!flat) return 'Untitled conversation';
  return flat.length <= max ? flat : `${flat.slice(0, max - 1).trimEnd()}…`;
}
