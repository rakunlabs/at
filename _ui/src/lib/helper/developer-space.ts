import type { Extension } from '@codemirror/state';
import type { DeveloperContentBlock, DeveloperSessionMessage } from '../api/developer-spaces';

export interface DeveloperTerminalTab { id: string; cwd: string; generation: number }

/** Parent folder of a space path ("" for top level). */
export function parentPath(path: string): string {
  const index = path.lastIndexOf('/');
  return index === -1 ? '' : path.slice(0, index);
}

export function baseName(path: string): string {
  return path.slice(path.lastIndexOf('/') + 1);
}

export function joinPath(folder: string, name: string): string {
  return folder ? `${folder}/${name}` : name;
}

/** The project a path belongs to: its first segment. */
export function projectOf(path: string): string {
  const index = path.indexOf('/');
  return index === -1 ? path : path.slice(0, index);
}

/** True when `path` is `folder` or inside it. */
export function isWithin(path: string, folder: string): boolean {
  return folder === '' || path === folder || path.startsWith(folder + '/');
}

/** Rewrites `path` after `from` was renamed to `to`; unrelated paths are unchanged. */
export function renamedPath(path: string, from: string, to: string): string {
  if (path === from) return to;
  if (path.startsWith(from + '/')) return to + path.slice(from.length);
  return path;
}

/**
 * Validates a name typed into the tree. A name is one segment; nesting is
 * expressed by creating inside a folder, so "a/b" here is a mistake.
 */
export function validEntryName(name: string): string | null {
  const trimmed = name.trim();
  if (!trimmed) return 'Enter a name.';
  if (trimmed === '.' || trimmed === '..') return 'That name is reserved.';
  if (/[/\\\0]/.test(trimmed)) return 'Names cannot contain slashes.';
  return null;
}

const IMAGE_EXT = new Set(['png', 'jpg', 'jpeg', 'gif', 'webp', 'bmp', 'ico', 'avif']);

export function extensionOf(path: string): string {
  const name = baseName(path).toLowerCase();
  const dot = name.lastIndexOf('.');
  return dot <= 0 ? name : name.slice(dot + 1);
}

export function isImagePath(path: string): boolean {
  return IMAGE_EXT.has(extensionOf(path));
}

export function isMarkdownFile(path: string): boolean {
  return ['md', 'markdown', 'mdx'].includes(extensionOf(path));
}

type LegacyMode = Parameters<typeof import('@codemirror/language').StreamLanguage.define>[0];

async function legacy(load: () => Promise<LegacyMode>): Promise<Extension> {
  const [{ StreamLanguage }, mode] = await Promise.all([import('@codemirror/language'), load()]);
  return StreamLanguage.define(mode);
}

/** Syntax mode for a file, loaded lazily. Unknown types get plain text. */
export async function languageFor(path: string): Promise<Extension> {
  const ext = extensionOf(path);
  const name = baseName(path).toLowerCase();
  switch (ext) {
    case 'js': case 'mjs': case 'cjs': case 'jsx':
      return (await import('@codemirror/lang-javascript')).javascript({ jsx: true });
    case 'ts': case 'mts': case 'cts': case 'tsx':
      return (await import('@codemirror/lang-javascript')).javascript({ typescript: true, jsx: ext === 'tsx' });
    case 'svelte': case 'vue': case 'html': case 'htm': case 'xml': case 'svg':
      return legacy(async () => (await import('@codemirror/legacy-modes/mode/xml')).html);
    case 'json': case 'jsonc':
      return (await import('@codemirror/lang-json')).json();
    case 'py':
      return (await import('@codemirror/lang-python')).python();
    case 'go':
      return legacy(async () => (await import('@codemirror/legacy-modes/mode/go')).go);
    case 'rs':
      return legacy(async () => (await import('@codemirror/legacy-modes/mode/rust')).rust);
    case 'sh': case 'bash': case 'zsh':
      return legacy(async () => (await import('@codemirror/legacy-modes/mode/shell')).shell);
    case 'yml': case 'yaml':
      return legacy(async () => (await import('@codemirror/legacy-modes/mode/yaml')).yaml);
    case 'toml':
      return legacy(async () => (await import('@codemirror/legacy-modes/mode/toml')).toml);
    case 'css': case 'scss': case 'less':
      return legacy(async () => (await import('@codemirror/legacy-modes/mode/css')).css);
    case 'sql':
      return legacy(async () => (await import('@codemirror/legacy-modes/mode/sql')).standardSQL);
    case 'rb':
      return legacy(async () => (await import('@codemirror/legacy-modes/mode/ruby')).ruby);
    case 'lua':
      return legacy(async () => (await import('@codemirror/legacy-modes/mode/lua')).lua);
    case 'java': case 'kt':
      return legacy(async () => (await import('@codemirror/legacy-modes/mode/clike')).java);
    case 'c': case 'h': case 'cpp': case 'hpp': case 'cc':
      return legacy(async () => (await import('@codemirror/legacy-modes/mode/clike')).cpp);
    case 'md': case 'markdown': case 'mdx':
      return [];
    case 'diff': case 'patch':
      return legacy(async () => (await import('@codemirror/legacy-modes/mode/diff')).diff);
  }
  if (name === 'dockerfile' || name.startsWith('dockerfile.')) {
    return legacy(async () => (await import('@codemirror/legacy-modes/mode/dockerfile')).dockerFile);
  }
  if (name === 'makefile') {
    return legacy(async () => (await import('@codemirror/legacy-modes/mode/shell')).shell);
  }
  return [];
}

// ─── Transcript ───

export interface TranscriptToolCall {
  id: string;
  name: string;
  input: Record<string, unknown>;
  result?: string;
  failed?: boolean;
}

export interface TranscriptEntry {
  key: string;
  role: 'user' | 'assistant' | 'notice';
  text: string;
  thinking?: string;
  tools: TranscriptToolCall[];
  created_at?: string;
}

function blockText(value: unknown): string {
  if (typeof value === 'string') return value;
  if (Array.isArray(value)) return value.map(part => (part && typeof part === 'object' && 'text' in part ? String((part as { text?: unknown }).text ?? '') : '')).join('');
  if (value == null) return '';
  return JSON.stringify(value);
}

/**
 * Turns stored canonical messages into chat bubbles. Tool results are attached
 * to the call that produced them (by tool_use_id) rather than rendered as
 * separate rows, and internal system nudges are hidden.
 */
export function buildTranscript(messages: DeveloperSessionMessage[]): TranscriptEntry[] {
  const entries: TranscriptEntry[] = [];
  const calls = new Map<string, TranscriptToolCall>();
  for (const message of messages) {
    if (message.role === 'system') continue;
    const blocks: DeveloperContentBlock[] = typeof message.content === 'string' ? [{ type: 'text', text: message.content }] : message.content ?? [];
    if (message.role === 'tool') {
      for (const block of blocks) {
        if (block.type !== 'tool_result' || !block.tool_use_id) continue;
        const call = calls.get(block.tool_use_id);
        if (!call) continue;
        call.result = blockText(block.content);
        call.failed = /^tool (error|denied|execution rejected)/.test(call.result);
      }
      continue;
    }
    const entry: TranscriptEntry = { key: message.id, role: message.role === 'user' ? 'user' : 'assistant', text: '', tools: [], created_at: message.created_at };
    for (const block of blocks) {
      if (block.type === 'text' && block.text) entry.text += (entry.text ? '\n\n' : '') + block.text;
      else if (block.type === 'thinking' && block.thinking) entry.thinking = (entry.thinking ?? '') + block.thinking;
      else if (block.type === 'tool_use' && block.id) {
        const call: TranscriptToolCall = { id: block.id, name: block.name ?? 'tool', input: block.input ?? {} };
        calls.set(block.id, call);
        entry.tools.push(call);
      }
    }
    // Consecutive assistant turns (text, tools, more text) read as one reply.
    const previous = entries[entries.length - 1];
    if (entry.role === 'assistant' && previous?.role === 'assistant') {
      if (entry.text) previous.text += (previous.text ? '\n\n' : '') + entry.text;
      if (entry.thinking) previous.thinking = (previous.thinking ?? '') + entry.thinking;
      previous.tools.push(...entry.tools);
      continue;
    }
    entries.push(entry);
  }
  return entries;
}

/** One-line summary of a tool call for its collapsed row. */
export function toolSummary(call: { name: string; input: Record<string, unknown> }): string {
  const input = call.input ?? {};
  const str = (key: string) => (typeof input[key] === 'string' ? (input[key] as string) : '');
  switch (call.name) {
    case 'read_file': case 'write_file': case 'edit_file': return str('path');
    case 'list_files': return str('path') || '.';
    case 'search': return str('pattern') + (str('path') ? ` in ${str('path')}` : '');
    case 'run_command': {
      const args = Array.isArray(input.args) ? (input.args as unknown[]).map(String) : [];
      return [str('command'), ...args].join(' ');
    }
    case 'ask_user': return str('question');
    case 'git_diff': return input.staged ? 'staged' : '';
    default: return '';
  }
}

export const MODE_LABELS: Record<string, string> = { plan: 'Plan', build: 'Build', review: 'Review' };
export const MODE_HINTS: Record<string, string> = {
  plan: 'Reads the project and proposes a plan. Cannot edit files or run commands.',
  build: 'Edits files. Asks before running commands.',
  review: 'Reviews changes. Cannot edit files or run commands.',
};

export const STATUS_LABELS: Record<string, string> = {
  idle: 'New', running: 'Working', waiting_permission: 'Needs approval', waiting_question: 'Has a question',
  completed: 'Done', failed: 'Failed', cancelled: 'Stopped',
};
