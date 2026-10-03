// Pure helpers for the trace explorer: observation tree, waterfall geometry,
// chat-message extraction from request/response bodies, and the filter ↔ URL
// mapping. No Svelte or DOM, so they are unit-tested directly.

/** The observation fields these helpers read (a subset of LLMCall). */
export interface TraceObservation {
  id: string;
  observation_type?: string;
  parent_observation_id?: string;
  name?: string;
  provider?: string;
  model?: string;
  started_at?: string;
  ended_at?: string;
  created_at?: string;
  latency_ms?: number;
  status?: string;
  level?: string;
  input_tokens?: number;
  output_tokens?: number;
  cost_cents?: number;
  metadata?: Record<string, unknown>;
}

export interface TreeNode<T extends TraceObservation = TraceObservation> {
  obs: T;
  depth: number;
  children: TreeNode<T>[];
  /** Earliest start / latest end across the subtree, in epoch ms. */
  start: number;
  end: number;
  /** Aggregates over the subtree (this observation included). */
  tokens: number;
  costCents: number;
  errors: number;
}

export function observationType(o: TraceObservation): string {
  return o.observation_type || 'generation';
}

export function isErrorObservation(o: TraceObservation): boolean {
  return o.status === 'error' || o.level === 'error';
}

export function observationLabel(o: TraceObservation): string {
  const type = observationType(o);
  if ((type === 'generation' || type === 'embedding') && !o.name) {
    return o.model ? (o.provider ? `${o.provider}/${o.model}` : o.model) : type;
  }
  return o.name || type;
}

function parseTime(value?: string): number {
  if (!value) return NaN;
  const t = Date.parse(value);
  return Number.isFinite(t) ? t : NaN;
}

/** Start and end of an observation in epoch ms, tolerant of older rows. */
export function observationTimes(o: TraceObservation): { start: number; end: number } {
  let end = parseTime(o.ended_at);
  if (!Number.isFinite(end)) end = parseTime(o.created_at);
  if (!Number.isFinite(end)) end = 0;
  let start = parseTime(o.started_at);
  if (!Number.isFinite(start) || start > end) start = end - Math.max(o.latency_ms || 0, 0);
  return { start, end };
}

/**
 * Builds the observation tree. Observations whose parent is not in the set
 * (a parent outside a truncated read, or a cross-trace link) become roots, so
 * nothing is ever dropped. Siblings are ordered by start time; cycles are
 * broken by refusing to attach a node under its own descendant.
 */
export function buildObservationTree<T extends TraceObservation>(observations: T[]): TreeNode<T>[] {
  const nodes = new Map<string, TreeNode<T>>();
  for (const obs of observations) {
    const { start, end } = observationTimes(obs);
    nodes.set(obs.id, { obs, depth: 0, children: [], start, end, tokens: 0, costCents: 0, errors: 0 });
  }
  const roots: TreeNode<T>[] = [];
  const parentOf = new Map<string, string>();
  for (const node of nodes.values()) {
    const parentID = node.obs.parent_observation_id;
    if (!parentID || parentID === node.obs.id || !nodes.has(parentID)) {
      roots.push(node);
      continue;
    }
    // Walk up from the candidate parent; attaching under a descendant would loop.
    let cursor: string | undefined = parentID;
    let cyclic = false;
    for (let hops = 0; cursor && hops <= nodes.size; hops++) {
      if (cursor === node.obs.id) { cyclic = true; break; }
      cursor = parentOf.get(cursor);
    }
    if (cyclic) {
      roots.push(node);
      continue;
    }
    parentOf.set(node.obs.id, parentID);
    nodes.get(parentID)!.children.push(node);
  }
  const byStart = (a: TreeNode<T>, b: TreeNode<T>) => a.start - b.start || a.obs.id.localeCompare(b.obs.id);
  const finish = (node: TreeNode<T>, depth: number) => {
    node.depth = depth;
    node.children.sort(byStart);
    node.tokens = (node.obs.input_tokens || 0) + (node.obs.output_tokens || 0);
    node.costCents = node.obs.cost_cents || 0;
    node.errors = isErrorObservation(node.obs) ? 1 : 0;
    for (const child of node.children) {
      finish(child, depth + 1);
      node.start = Math.min(node.start, child.start);
      node.end = Math.max(node.end, child.end);
      node.tokens += child.tokens;
      node.costCents += child.costCents;
      node.errors += child.errors;
    }
  };
  roots.sort(byStart);
  for (const root of roots) finish(root, 0);
  return roots;
}

/** Depth-first rows, skipping the children of collapsed nodes. */
export function flattenTree<T extends TraceObservation>(roots: TreeNode<T>[], collapsed: Set<string> = new Set()): TreeNode<T>[] {
  const rows: TreeNode<T>[] = [];
  const visit = (node: TreeNode<T>) => {
    rows.push(node);
    if (!collapsed.has(node.obs.id)) node.children.forEach(visit);
  };
  roots.forEach(visit);
  return rows;
}

/** IDs of every ancestor of id, for expanding a deep-linked observation. */
export function ancestorIDs<T extends TraceObservation>(roots: TreeNode<T>[], id: string): string[] {
  const path: string[] = [];
  const visit = (node: TreeNode<T>): boolean => {
    if (node.obs.id === id) return true;
    path.push(node.obs.id);
    if (node.children.some(visit)) return true;
    path.pop();
    return false;
  };
  roots.some(visit);
  return path;
}

export interface Timeline {
  start: number;
  end: number;
  duration: number;
}

export function timelineOf<T extends TraceObservation>(roots: TreeNode<T>[]): Timeline {
  if (roots.length === 0) return { start: 0, end: 0, duration: 0 };
  const start = Math.min(...roots.map((r) => r.start));
  const end = Math.max(...roots.map((r) => r.end));
  return { start, end, duration: Math.max(end - start, 0) };
}

/** Bar offset and width in percent of the timeline, with a visible minimum. */
export function barGeometry(timeline: Timeline, start: number, end: number, minWidth = 0.4): { left: number; width: number } {
  if (timeline.duration <= 0) return { left: 0, width: 100 };
  const left = Math.min(Math.max(((start - timeline.start) / timeline.duration) * 100, 0), 100);
  let width = Math.max(((end - start) / timeline.duration) * 100, minWidth);
  if (left + width > 100) width = Math.max(100 - left, minWidth);
  return { left, width };
}

/** Evenly spaced axis ticks (ms offsets from the timeline start). */
export function timelineTicks(duration: number, count = 5): number[] {
  if (duration <= 0) return [0];
  const raw = duration / count;
  const magnitude = 10 ** Math.floor(Math.log10(raw));
  const step = [1, 2, 5, 10].map((m) => m * magnitude).find((s) => s >= raw) || raw;
  const ticks: number[] = [];
  for (let t = 0; t <= duration + 1e-9; t += step) ticks.push(t);
  return ticks;
}

// ─── Chat messages ───

export interface ChatToolCall {
  id?: string;
  name: string;
  arguments: string;
}

export interface ChatMessage {
  role: string;
  text: string;
  /** Reasoning / thinking text, rendered collapsed. */
  reasoning?: string;
  toolCalls: ChatToolCall[];
  /** For tool results: the call this answers. */
  toolCallID?: string;
  /** Tool results carried as blocks inside a user/tool message. */
  toolResults: { toolCallID?: string; content: string; isError?: boolean }[];
  /** Placeholders for non-text parts (images, documents). */
  attachments: string[];
}

export interface ParsedConversation {
  system: string;
  messages: ChatMessage[];
  tools: { name: string; description?: string }[];
  model?: string;
}

function stringify(value: unknown): string {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function emptyMessage(role: string): ChatMessage {
  return { role, text: '', toolCalls: [], toolResults: [], attachments: [] };
}

/** Folds one content value (string or block array) into a message. */
function applyContent(msg: ChatMessage, content: unknown) {
  if (content === undefined || content === null) return;
  if (typeof content === 'string') {
    msg.text = msg.text ? `${msg.text}\n${content}` : content;
    return;
  }
  if (!Array.isArray(content)) {
    msg.text = stringify(content);
    return;
  }
  const texts: string[] = [];
  for (const block of content as Record<string, any>[]) {
    if (!block || typeof block !== 'object') continue;
    switch (block.type) {
      case 'text':
      case 'input_text':
      case 'output_text':
        if (typeof block.text === 'string') texts.push(block.text);
        break;
      case 'thinking':
      case 'reasoning':
        msg.reasoning = [msg.reasoning, block.thinking || block.text].filter(Boolean).join('\n');
        break;
      case 'tool_use':
        msg.toolCalls.push({ id: block.id, name: block.name || 'tool', arguments: stringify(block.input ?? {}) });
        break;
      case 'tool_result':
        msg.toolResults.push({
          toolCallID: block.tool_use_id,
          content: typeof block.content === 'string' ? block.content : contentToText(block.content),
          isError: block.is_error === true,
        });
        break;
      case 'image':
      case 'image_url':
      case 'input_image':
        msg.attachments.push('image');
        break;
      case 'document':
      case 'file':
      case 'input_file':
        msg.attachments.push(block.name || block.filename || 'document');
        break;
      default:
        if (typeof block.text === 'string') texts.push(block.text);
    }
  }
  if (texts.length) msg.text = [msg.text, ...texts].filter(Boolean).join('\n');
}

function contentToText(content: unknown): string {
  if (typeof content === 'string') return content;
  if (!Array.isArray(content)) return stringify(content);
  return (content as Record<string, any>[])
    .map((b) => (typeof b?.text === 'string' ? b.text : b?.type === 'image' ? '[image]' : stringify(b)))
    .join('\n');
}

function parseJSON(raw: string | undefined): any {
  if (!raw) return undefined;
  try {
    return JSON.parse(raw);
  } catch {
    return undefined;
  }
}

/**
 * Extracts a conversation from a generation's request body. Understands the
 * OpenAI chat and Responses shapes, Anthropic Messages (top-level system), and
 * AT's canonical agent-loop shape (Anthropic-style blocks under `messages`).
 * Returns null when the body is not a recognizable conversation.
 */
export function parseRequestConversation(raw: string | undefined): ParsedConversation | null {
  const body = parseJSON(raw);
  if (!body || typeof body !== 'object') return null;
  const conv: ParsedConversation = { system: '', messages: [], tools: [], model: typeof body.model === 'string' ? body.model : undefined };
  if (body.system !== undefined) conv.system = contentToText(body.system);
  if (typeof body.instructions === 'string') conv.system = body.instructions;
  if (Array.isArray(body.tools)) {
    for (const tool of body.tools) {
      const name = tool?.function?.name || tool?.name;
      if (name) conv.tools.push({ name, description: tool?.function?.description || tool?.description });
    }
  }
  const items: any[] | undefined = Array.isArray(body.messages)
    ? body.messages
    : Array.isArray(body.input)
      ? body.input
      : typeof body.input === 'string'
        ? [{ role: 'user', content: body.input }]
        : undefined;
  if (!items) return null;
  for (const item of items) {
    if (!item || typeof item !== 'object') continue;
    // Responses API function call items.
    if (item.type === 'function_call') {
      const last = conv.messages.at(-1);
      const target = last && last.role === 'assistant' ? last : (conv.messages.push(emptyMessage('assistant')), conv.messages.at(-1)!);
      target.toolCalls.push({ id: item.call_id, name: item.name || 'tool', arguments: typeof item.arguments === 'string' ? item.arguments : stringify(item.arguments) });
      continue;
    }
    if (item.type === 'function_call_output') {
      const msg = emptyMessage('tool');
      msg.toolCallID = item.call_id;
      msg.text = stringify(item.output);
      conv.messages.push(msg);
      continue;
    }
    const role = typeof item.role === 'string' ? item.role : 'user';
    if ((role === 'system' || role === 'developer') && conv.messages.length === 0 && !conv.system) {
      conv.system = contentToText(item.content);
      continue;
    }
    const msg = emptyMessage(role);
    applyContent(msg, item.content);
    if (typeof item.reasoning_content === 'string') msg.reasoning = item.reasoning_content;
    if (Array.isArray(item.tool_calls)) {
      for (const call of item.tool_calls) {
        msg.toolCalls.push({ id: call.id, name: call.function?.name || call.name || 'tool', arguments: call.function?.arguments ?? stringify(call.arguments) });
      }
    }
    if (typeof item.tool_call_id === 'string') msg.toolCallID = item.tool_call_id;
    conv.messages.push(msg);
  }
  return conv;
}

/**
 * Extracts the assistant turn from a generation's response body: OpenAI chat
 * completions, Anthropic Messages, the Responses API, and AT's serialized
 * service.LLMResponse (Go field names).
 */
export function parseResponseMessage(raw: string | undefined): ChatMessage | null {
  const body = parseJSON(raw);
  if (!body || typeof body !== 'object') return null;
  const msg = emptyMessage('assistant');
  if (Array.isArray(body.choices)) {
    const m = body.choices[0]?.message || body.choices[0]?.delta;
    if (!m) return null;
    applyContent(msg, m.content);
    if (typeof m.reasoning_content === 'string') msg.reasoning = m.reasoning_content;
    if (typeof m.refusal === 'string' && m.refusal) msg.text = [msg.text, `[refusal] ${m.refusal}`].filter(Boolean).join('\n');
    for (const call of m.tool_calls || []) {
      msg.toolCalls.push({ id: call.id, name: call.function?.name || 'tool', arguments: call.function?.arguments ?? '' });
    }
    return msg;
  }
  if (Array.isArray(body.content) && (body.type === 'message' || body.role === 'assistant')) {
    applyContent(msg, body.content);
    return msg;
  }
  if (Array.isArray(body.output)) {
    for (const item of body.output) {
      if (item?.type === 'message') applyContent(msg, item.content);
      else if (item?.type === 'function_call') msg.toolCalls.push({ id: item.call_id, name: item.name, arguments: item.arguments ?? '' });
      else if (item?.type === 'reasoning') msg.reasoning = contentToText(item.summary ?? item.content);
    }
    return msg;
  }
  if ('Content' in body || 'ToolCalls' in body) {
    msg.text = typeof body.Content === 'string' ? body.Content : '';
    if (typeof body.ReasoningContent === 'string' && body.ReasoningContent) msg.reasoning = body.ReasoningContent;
    if (typeof body.Refusal === 'string' && body.Refusal) msg.text = [msg.text, `[refusal] ${body.Refusal}`].filter(Boolean).join('\n');
    for (const call of body.ToolCalls || []) {
      msg.toolCalls.push({ id: call.ID, name: call.Name || 'tool', arguments: stringify(call.Arguments ?? {}) });
    }
    return msg;
  }
  return null;
}

/** Reads optional model parameters from a request body. */
export function requestParameters(raw: string | undefined): Record<string, unknown> {
  const body = parseJSON(raw);
  if (!body || typeof body !== 'object') return {};
  const out: Record<string, unknown> = {};
  for (const key of ['temperature', 'top_p', 'max_tokens', 'max_completion_tokens', 'max_output_tokens', 'reasoning_effort', 'seed', 'stream', 'tool_choice', 'response_format', 'stop', 'n', 'presence_penalty', 'frequency_penalty']) {
    if (body[key] !== undefined) out[key] = body[key];
  }
  if (body.reasoning?.effort) out.reasoning_effort = body.reasoning.effort;
  if (body.thinking) out.thinking = body.thinking;
  return out;
}

// ─── Filters ───

export interface TraceFilters {
  q: string;
  from: string;
  to: string;
  name: string[];
  model: string[];
  source: string[];
  tag: string[];
  environment: string[];
  release: string[];
  user_id: string[];
  end_user: string[];
  session_id: string[];
  task_id: string[];
  status: '' | 'ok' | 'error';
  min_latency_ms: string;
  max_latency_ms: string;
  min_cost_cents: string;
  max_cost_cents: string;
  min_tokens: string;
  max_tokens: string;
  score_name: string;
  min_score: string;
  max_score: string;
  bookmarked: boolean;
  sort: string;
  order: 'asc' | 'desc';
}

export const listFilterKeys = ['name', 'model', 'source', 'tag', 'environment', 'release', 'user_id', 'end_user', 'session_id', 'task_id'] as const;
export const scalarFilterKeys = ['q', 'from', 'to', 'status', 'min_latency_ms', 'max_latency_ms', 'min_cost_cents', 'max_cost_cents', 'min_tokens', 'max_tokens', 'score_name', 'min_score', 'max_score', 'sort', 'order'] as const;

export function emptyTraceFilters(): TraceFilters {
  return {
    q: '', from: '', to: '', name: [], model: [], source: [], tag: [], environment: [], release: [],
    user_id: [], end_user: [], session_id: [], task_id: [], status: '',
    min_latency_ms: '', max_latency_ms: '', min_cost_cents: '', max_cost_cents: '', min_tokens: '', max_tokens: '',
    score_name: '', min_score: '', max_score: '', bookmarked: false, sort: '', order: 'desc',
  };
}

/** Reads filters from a URL query (lists are comma separated). */
export function filtersFromQuery(query: URLSearchParams): TraceFilters {
  const f = emptyTraceFilters();
  for (const key of listFilterKeys) {
    f[key] = (query.get(key) || '').split(',').map((v) => v.trim()).filter(Boolean);
  }
  for (const key of scalarFilterKeys) {
    const v = query.get(key);
    if (v !== null) (f as any)[key] = v;
  }
  if (f.status !== 'ok' && f.status !== 'error') f.status = '';
  if (f.order !== 'asc') f.order = 'desc';
  // Legacy deep link from TaskDetail.
  if (!f.task_id.length && query.get('task_ids')) f.task_id = query.get('task_ids')!.split(',').filter(Boolean);
  f.bookmarked = query.get('bookmarked') === 'true';
  return f;
}

/** Writes filters as URL query values; empty values clear their key. */
export function filtersToQuery(f: TraceFilters): Record<string, string | null> {
  const out: Record<string, string | null> = {};
  for (const key of listFilterKeys) out[key] = f[key].length ? f[key].join(',') : null;
  for (const key of scalarFilterKeys) out[key] = (f as any)[key] || null;
  if (f.order === 'desc') out.order = null;
  out.bookmarked = f.bookmarked ? 'true' : null;
  out.task_ids = null;
  return out;
}

/** API parameters for GET /traces. */
export function filtersToParams(f: TraceFilters): Record<string, string | string[]> {
  const out: Record<string, string | string[]> = {};
  for (const key of listFilterKeys) if (f[key].length) out[key] = f[key];
  for (const key of scalarFilterKeys) if ((f as any)[key]) out[key] = (f as any)[key];
  if (f.bookmarked) out.bookmarked = 'true';
  return out;
}

export interface FilterChip {
  key: string;
  label: string;
  /** For list filters, the single value this chip removes. */
  value?: string;
}

const chipNames: Record<string, string> = {
  name: 'Name', model: 'Model', source: 'Source', tag: 'Tag', environment: 'Env', release: 'Release',
  user_id: 'User', end_user: 'End user', session_id: 'Session', task_id: 'Task',
};

/** Active filters as removable chips (search, time and sort are separate controls). */
export function filterChips(f: TraceFilters): FilterChip[] {
  const chips: FilterChip[] = [];
  for (const key of listFilterKeys) for (const value of f[key]) chips.push({ key, value, label: `${chipNames[key]}: ${value}` });
  if (f.status) chips.push({ key: 'status', label: f.status === 'error' ? 'Has errors' : 'No errors' });
  const range = (key: string, label: string, min: string, max: string, unit: (v: string) => string) => {
    if (min && max) chips.push({ key, label: `${unit(min)} ≤ ${label} ≤ ${unit(max)}` });
    else if (min) chips.push({ key, label: `${label} ≥ ${unit(min)}` });
    else if (max) chips.push({ key, label: `${label} ≤ ${unit(max)}` });
  };
  range('latency', 'Latency', f.min_latency_ms, f.max_latency_ms, (v) => formatDurationMs(Number(v)));
  range('cost', 'Cost', f.min_cost_cents, f.max_cost_cents, (v) => formatCost(Number(v)));
  range('tokens', 'Tokens', f.min_tokens, f.max_tokens, (v) => formatTokens(Number(v)));
  if (f.score_name) {
    const bounds = [f.min_score && `≥ ${f.min_score}`, f.max_score && `≤ ${f.max_score}`].filter(Boolean).join(' ');
    chips.push({ key: 'score', label: `Score ${f.score_name}${bounds ? ' ' + bounds : ''}` });
  }
  if (f.bookmarked) chips.push({ key: 'bookmarked', label: 'Bookmarked' });
  return chips;
}

export function removeChip(f: TraceFilters, chip: FilterChip): TraceFilters {
  const next: TraceFilters = { ...f, ...Object.fromEntries(listFilterKeys.map((k) => [k, [...f[k]]])) } as TraceFilters;
  if ((listFilterKeys as readonly string[]).includes(chip.key)) {
    const key = chip.key as (typeof listFilterKeys)[number];
    next[key] = next[key].filter((v) => v !== chip.value);
  } else if (chip.key === 'latency') {
    next.min_latency_ms = next.max_latency_ms = '';
  } else if (chip.key === 'cost') {
    next.min_cost_cents = next.max_cost_cents = '';
  } else if (chip.key === 'tokens') {
    next.min_tokens = next.max_tokens = '';
  } else if (chip.key === 'score') {
    next.score_name = next.min_score = next.max_score = '';
  } else if (chip.key === 'status') {
    next.status = '';
  } else if (chip.key === 'bookmarked') {
    next.bookmarked = false;
  }
  return next;
}

// ─── Formatting ───

export function formatDurationMs(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return '0ms';
  if (ms < 1000) return `${Math.round(ms)}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)}s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return `${minutes}m ${seconds}s`;
}

export function formatCost(cents: number): string {
  if (!Number.isFinite(cents) || cents === 0) return '$0';
  const dollars = cents / 100;
  if (dollars < 0.01) return `$${dollars.toFixed(5)}`;
  if (dollars < 1) return `$${dollars.toFixed(4)}`;
  return `$${dollars.toFixed(2)}`;
}

export function formatTokens(n: number): string {
  if (!Number.isFinite(n) || n === 0) return '0';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 10_000) return `${(n / 1_000).toFixed(1)}k`;
  return n.toLocaleString('en-US');
}

export function formatScore(s: { data_type: string; average?: number; value?: number; string_value?: string }): string {
  if (s.data_type === 'categorical') return s.string_value || '–';
  const v = s.average ?? s.value;
  if (v === undefined || v === null) return '–';
  if (s.data_type === 'boolean') return v >= 0.5 ? 'true' : 'false';
  return Number.isInteger(v) ? String(v) : v.toFixed(2);
}

/**
 * Compact local timestamp for dense tables: time with milliseconds today,
 * prefixed by the date otherwise.
 */
export function formatTraceTime(value: string | undefined, now = new Date()): string {
  if (!value) return '–';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '–';
  const pad = (n: number, w = 2) => String(n).padStart(w, '0');
  const time = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`;
  const sameDay = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  return sameDay ? time : `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${time}`;
}

/** Time-range presets for the toolbar. */
export const timePresets = [
  { key: '1h', label: 'Last hour', ms: 3_600_000 },
  { key: '24h', label: 'Last 24h', ms: 86_400_000 },
  { key: '7d', label: 'Last 7 days', ms: 7 * 86_400_000 },
  { key: '30d', label: 'Last 30 days', ms: 30 * 86_400_000 },
] as const;

export function presetFrom(key: string, now = Date.now()): string {
  const preset = timePresets.find((p) => p.key === key);
  return preset ? new Date(now - preset.ms).toISOString() : '';
}
