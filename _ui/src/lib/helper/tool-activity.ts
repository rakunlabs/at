import type { ChatMessage } from './chat';

/** Match within an assistant turn, not by name or a globally unique ID assumption. */
export function toolResultsByMessage(messages: ChatMessage[]): Map<number, Map<string, ChatMessage>> {
  const results = new Map<number, Map<string, ChatMessage>>();
  let owner = -1;
  let ids = new Set<string>();
  messages.forEach((message, index) => {
    if (message.role === 'assistant') {
      owner = index;
      ids = new Set(message.tool_calls?.map(call => call.id) || []);
      if (ids.size) results.set(owner, new Map());
    } else if (message.role === 'tool') {
      if (message.tool_call_id && ids.has(message.tool_call_id)) results.get(owner)?.set(message.tool_call_id, message);
    } else {
      owner = -1;
      ids = new Set();
    }
  });
  return results;
}

export function formatToolPayload(value: string): string {
  try { return JSON.stringify(JSON.parse(value), null, 2); }
  catch { return value; }
}

export function toolResultFailed(value: string): boolean {
  if (/^Error:/i.test(value.trimStart())) return true;
  try {
    const parsed = JSON.parse(value);
    return !!parsed && typeof parsed === 'object' && (parsed.isError === true || !!parsed.error);
  } catch { return false; }
}

/** One-character marker for a tool row, by what the tool does. */
export function toolGlyph(name: string): string {
  const n = name.toLowerCase();
  if (/(bash|shell|exec|command|terminal)/.test(n)) return '$';
  if (/(edit|write|patch|update|create|set_|save|delete|remove)/.test(n)) return '✎';
  if (/(read|get|fetch|open|load|view|cat)/.test(n)) return '→';
  if (/(grep|glob|search|find|list|query)/.test(n)) return '*';
  if (/(agent|delegate|skill|run)/.test(n)) return '◇';
  return '•';
}

const ARG_PRIORITY = ['command', 'path', 'file_path', 'filePath', 'file', 'pattern', 'query', 'url', 'name', 'skill_name', 'id', 'task', 'prompt'];

/**
 * The argument a reader identifies a call by, e.g. the path of a read or the
 * command of a shell call. Falls back to the first scalar argument.
 */
export function toolArgSummary(raw: string, max = 120): string {
  let args: unknown;
  try { args = JSON.parse(raw); } catch { return clip(raw.trim(), max); }
  if (!args || typeof args !== 'object' || Array.isArray(args)) return '';
  const record = args as Record<string, unknown>;
  const key = ARG_PRIORITY.find(k => isScalar(record[k])) ?? Object.keys(record).find(k => isScalar(record[k]));
  if (!key) return '';
  const value = String(record[key]).replace(/\s+/g, ' ').trim();
  const quoted = key === 'pattern' || key === 'query' ? `"${value}"` : value;
  return clip(quoted, max);
}

/** A short parenthetical describing the result, e.g. `12 lines` or `error`. */
export function toolResultSummary(output: string, failed: boolean): string {
  if (failed) {
    const first = output.trim().split('\n')[0].replace(/^Error:\s*/i, '');
    return first ? `error · ${clip(first, 60)}` : 'error';
  }
  const text = output.trim();
  if (!text) return 'empty';
  const lines = text.split('\n').length;
  return lines === 1 ? clip(text, 48) : `${lines} lines`;
}

function isScalar(v: unknown): v is string | number | boolean {
  return (typeof v === 'string' && v.trim() !== '') || typeof v === 'number' || typeof v === 'boolean';
}

function clip(s: string, max: number): string {
  return s.length > max ? `${s.slice(0, max - 1)}…` : s;
}
