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

export interface TranscriptAttachment { name: string; mediaType: string; data: string }

export interface TranscriptEntry {
  key: string;
  role: 'user' | 'assistant' | 'notice';
  text: string;
  thinking?: string;
  tools: TranscriptToolCall[];
  created_at?: string;
  /** A user prompt with a file snapshot can be undone. */
  undoable?: boolean;
  /** A /compact summary: earlier messages no longer go to the model. */
  compaction?: boolean;
  attachments?: TranscriptAttachment[];
  /** Files mentioned with @ whose contents were sent with the prompt. */
  mentions?: string[];
}

function blockText(value: unknown): string {
  if (typeof value === 'string') return value;
  if (Array.isArray(value)) return value.map(part => (part && typeof part === 'object' && 'text' in part ? String((part as { text?: unknown }).text ?? '') : '')).join('');
  if (value == null) return '';
  return JSON.stringify(value);
}

const MENTION_PREFIX = /^Contents of @(\S+) \(numbered like read_file\):\n|^@(\S+) could not be read: /;

/**
 * Messages the reader sees: everything before an undo point. The hidden
 * remainder stays stored until redo or the next prompt.
 */
export function visibleMessages(messages: DeveloperSessionMessage[], revertMessageId = ''): DeveloperSessionMessage[] {
  if (!revertMessageId) return messages;
  const index = messages.findIndex(m => m.id === revertMessageId);
  return index < 0 ? messages : messages.slice(0, index);
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
        call.failed = /^tool (error|denied|execution rejected|not executed)/.test(call.result);
      }
      continue;
    }
    const entry: TranscriptEntry = { key: message.id, role: message.role === 'user' ? 'user' : 'assistant', text: '', tools: [], created_at: message.created_at };
    if (message.kind === 'compaction') entry.compaction = true;
    if (message.role === 'user' && message.snapshot) entry.undoable = true;
    let pendingName = '';
    for (const block of blocks) {
      if (block.type === 'text' && block.text) {
        if (message.role === 'user') {
          const mention = MENTION_PREFIX.exec(block.text);
          if (mention) { (entry.mentions ??= []).push(mention[1] ?? mention[2]); continue; }
          const attached = /^Attached file: (.+)$/.exec(block.text);
          if (attached) { pendingName = attached[1]; continue; }
          if (pendingName) {
            // A small text attachment is sent inline right after its label.
            (entry.attachments ??= []).push({ name: pendingName, mediaType: 'text/plain', data: '' });
            pendingName = '';
            continue;
          }
        }
        entry.text += (entry.text ? '\n\n' : '') + block.text;
      } else if ((block.type === 'image' || block.type === 'document') && block.source) {
        (entry.attachments ??= []).push({ name: pendingName || block.type, mediaType: block.source.media_type ?? '', data: block.source.data ?? '' });
        pendingName = '';
      } else if (block.type === 'thinking' && block.thinking) entry.thinking = (entry.thinking ?? '') + block.thinking;
      else if (block.type === 'tool_use' && block.id) {
        const call: TranscriptToolCall = { id: block.id, name: block.name ?? 'tool', input: block.input ?? {} };
        calls.set(block.id, call);
        entry.tools.push(call);
      }
    }
    // Consecutive assistant turns (text, tools, more text) read as one reply.
    const previous = entries[entries.length - 1];
    if (entry.role === 'assistant' && !entry.compaction && previous?.role === 'assistant' && !previous.compaction) {
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
    case 'glob': return str('pattern') + (str('path') ? ` in ${str('path')}` : '');
    case 'search': return str('pattern') + (str('include') ? ` (${str('include')})` : '') + (str('path') ? ` in ${str('path')}` : '');
    case 'bash': return str('command');
    case 'run_command': {
      const args = Array.isArray(input.args) ? (input.args as unknown[]).map(String) : [];
      return [str('command'), ...args].join(' ');
    }
    case 'todo_write': return Array.isArray(input.todos) ? `${input.todos.length} tasks` : '';
    case 'ask_user': return str('question');
    case 'git_diff': return input.staged ? 'staged' : '';
    default: return '';
  }
}

// ─── Composer: slash commands and @ mentions ───

/** Top-level folder holding undo snapshots (internal/devfs.SnapshotDir). */
export const SNAPSHOT_DIR = '.at-snapshots';

export interface DeveloperCommand { name: string; args?: string; description: string }

export const DEVELOPER_COMMANDS: DeveloperCommand[] = [
  { name: 'compact', args: '[focus]', description: 'Summarize the conversation so the model continues from the summary' },
  { name: 'undo', description: 'Undo the last prompt and restore the files it changed' },
  { name: 'redo', description: 'Bring back what the last undo removed' },
  { name: 'new', description: 'Start a new session in this project' },
  { name: 'model', description: 'Choose the model' },
  { name: 'agent', description: 'Choose the agent or profile' },
  { name: 'effort', args: '[level]', description: 'Set the reasoning effort' },
  { name: 'todos', description: 'Show the task list' },
];

/** `@partial` being typed at the caret, for the file picker; null otherwise. */
export function mentionQuery(text: string, caret: number): { start: number; query: string } | null {
  const before = text.slice(0, caret);
  const match = /(^|\s)@([^\s@]*)$/.exec(before);
  if (!match) return null;
  return { start: caret - match[2].length - 1, query: match[2] };
}

/** Files mentioned as `@path` in a prompt, restricted to known project files. */
export function promptMentions(text: string, files: ReadonlySet<string>): string[] {
  const found: string[] = [];
  for (const match of text.matchAll(/(?:^|\s)@([^\s@]+)/g)) {
    const path = match[1].replace(/[.,;:!?)]+$/, '');
    if (files.has(path) && !found.includes(path)) found.push(path);
  }
  return found;
}

/**
 * Ranks files for a fuzzy query like OpenCode's picker: every query character
 * must appear in order; matches in the base name, at segment starts and in a
 * run score higher, and shorter paths win ties.
 */
export function rankFiles(files: string[], query: string, limit = 50): string[] {
  const q = query.toLowerCase();
  if (!q) return files.slice(0, limit);
  const scored: Array<{ path: string; score: number }> = [];
  for (const path of files) {
    const lower = path.toLowerCase();
    const baseStart = lower.lastIndexOf('/') + 1;
    const base = lower.slice(baseStart);
    // A contiguous match in the file name is what the reader almost always
    // means; scoring it directly avoids greedy matching in parent folders.
    if (base.includes(q)) {
      scored.push({ path, score: 1000 + (base.startsWith(q) ? 100 : 0) - path.length });
      continue;
    }
    if (lower.includes(q)) {
      scored.push({ path, score: 500 - path.length });
      continue;
    }
    let score = 0, from = 0, previous = -2;
    let ok = true;
    for (const ch of q) {
      const index = lower.indexOf(ch, from);
      if (index < 0) { ok = false; break; }
      score += 1;
      if (index === previous + 1) score += 3;
      if (index >= baseStart) score += 2;
      if (index === 0 || lower[index - 1] === '/' || lower[index - 1] === '.' || lower[index - 1] === '-' || lower[index - 1] === '_') score += 4;
      previous = index;
      from = index + 1;
    }
    if (!ok) continue;
    scored.push({ path, score: score - path.length / 10 });
  }
  return scored.sort((a, b) => b.score - a.score).slice(0, limit).map(s => s.path);
}

export const MODE_LABELS: Record<string, string> = { plan: 'Plan', build: 'Build', review: 'Review' };
export const MODE_HINTS: Record<string, string> = {
  plan: 'Reads the project and proposes a plan. Cannot edit files or run commands.',
  build: 'Edits files. Asks before running shell commands.',
  review: 'Reviews changes. Cannot edit files or run commands.',
};

/** What the composer's agent picker offers: the built-in profiles plus the workspace's agents. */
export interface DeveloperAgentChoice { value: string; label: string; hint: string; group: string }

export const BUILTIN_AGENT_PREFIX = 'builtin:';

/** Picker value for a session: `agent:<id>` or `builtin:<mode>`. */
export function developerAgentValue(session: { agent_id?: string; mode: string }): string {
  return session.agent_id ? `agent:${session.agent_id}` : `${BUILTIN_AGENT_PREFIX}${session.mode}`;
}

/** The settings a picker value selects. Built-ins clear the agent. */
export function developerAgentSettings(value: string): { agent_id: string; mode?: 'plan' | 'build' | 'review' } {
  if (value.startsWith('agent:')) return { agent_id: value.slice('agent:'.length) };
  const mode = value.slice(BUILTIN_AGENT_PREFIX.length);
  return { agent_id: '', mode: (mode === 'plan' || mode === 'review' ? mode : 'build') };
}

export function developerAgentChoices(agents: Array<{ id: string; name: string; config?: { description?: string; group?: string } }>): DeveloperAgentChoice[] {
  const builtins = (['build', 'plan', 'review'] as const).map(mode => ({
    value: `${BUILTIN_AGENT_PREFIX}${mode}`, label: MODE_LABELS[mode], hint: MODE_HINTS[mode], group: 'Built-in',
  }));
  const custom = [...agents]
    .sort((a, b) => (a.config?.group || '').localeCompare(b.config?.group || '') || a.name.localeCompare(b.name))
    .map(agent => ({ value: `agent:${agent.id}`, label: agent.name, hint: agent.config?.description || '', group: agent.config?.group || 'Agents' }));
  return [...builtins, ...custom];
}

export const STATUS_LABELS: Record<string, string> = {
  idle: 'New', running: 'Working', waiting_permission: 'Needs approval', waiting_question: 'Has a question',
  completed: 'Done', failed: 'Failed', cancelled: 'Stopped',
};
