// The Developer Space tool source of Chats: tools that run in the reader's
// own Developer Space container. This module holds what the page needs and a
// test can check without a browser: which calls ask first, what a standing
// approval covers, and how a stored result carries its diff.

import type { DeveloperToolResult } from '../api/developer-spaces';

/**
 * A standing approval for one conversation: `<first word> *` for a command
 * ("npm *"), or the exact command when its first word is shell syntax.
 */
export function developerApprovalFor(command: string): string {
  const first = command.trim().split(/\s+/)[0] ?? '';
  if (first && !/[|;&$`()<>]/.test(first)) return `${first} *`;
  return command.trim();
}

/**
 * Whether a standing approval covers this command. A prefix grant never
 * covers a command that chains, pipes, substitutes or redirects into
 * something else, so `go *` does not allow `go test && rm -rf /`.
 */
export function developerCommandApproved(approvals: readonly string[], command: string): boolean {
  const trimmed = command.trim();
  for (const grant of approvals) {
    if (grant === trimmed && !grant.endsWith(' *')) return true;
    if (!grant.endsWith(' *')) continue;
    if (/[|;&$`()<>\n]/.test(trimmed)) continue;
    const prefix = grant.slice(0, -2);
    if (trimmed === prefix || trimmed.startsWith(`${prefix} `)) return true;
  }
  return false;
}

/** Calls that need the reader's yes before they run. */
export function developerCallNeedsApproval(name: string, args: Record<string, unknown>, approvals: readonly string[]): boolean {
  if (name === 'dev_bash_kill') return true;
  if (name !== 'dev_bash') return false;
  return !developerCommandApproved(approvals, String(args.command ?? ''));
}

const DIFF_OPEN = '\n\n<at-developer-diff>\n';
const DIFF_CLOSE = '\n</at-developer-diff>';

export interface DeveloperResultView {
  /** What the model saw, without the diff block. */
  text: string;
  project: string;
  changed: string[];
  diff: string;
  truncated: boolean;
}

/**
 * The stored tool result: the output the model reads, then the changed files
 * and diff in a marked block the page renders. The model sees the block too,
 * which is useful — it is the edit it just made — and bounded server-side.
 */
export function developerResultText(result: DeveloperToolResult): string {
  let text = result.error ? `Error: ${result.error}${result.output?.trim() ? `\n${result.output}` : ''}` : (result.output?.trim() ? result.output : '(no output)');
  if (result.changed?.length || result.diff) {
    const meta = JSON.stringify({ project: result.project ?? '', changed: result.changed ?? [], truncated: !!result.diff_truncated });
    text += `${DIFF_OPEN}${meta}\n${result.diff ?? ''}${DIFF_CLOSE}`;
  }
  return text;
}

/** Reads a stored result back; results without a diff block pass through. */
export function parseDeveloperResult(stored: string): DeveloperResultView {
  const open = stored.lastIndexOf(DIFF_OPEN);
  const close = stored.lastIndexOf(DIFF_CLOSE);
  if (open < 0 || close < open) return { text: stored, project: '', changed: [], diff: '', truncated: false };
  const body = stored.slice(open + DIFF_OPEN.length, close);
  const newline = body.indexOf('\n');
  let meta: { project?: unknown; changed?: unknown; truncated?: unknown } = {};
  try { meta = JSON.parse(newline < 0 ? body : body.slice(0, newline)); } catch { /* shown as plain text */ }
  return {
    text: stored.slice(0, open),
    project: typeof meta.project === 'string' ? meta.project : '',
    changed: Array.isArray(meta.changed) ? meta.changed.filter((p): p is string => typeof p === 'string') : [],
    diff: newline < 0 ? '' : body.slice(newline + 1),
    truncated: meta.truncated === true,
  };
}

/** Per-line class for a unified diff, matching the Developer Spaces viewer. */
export function diffLineClass(line: string): string {
  if (line.startsWith('+++') || line.startsWith('---')) return 'text-dark-text-muted';
  if (line.startsWith('+')) return 'bg-green-950/40 text-green-300';
  if (line.startsWith('-')) return 'bg-red-950/40 text-red-300';
  if (line.startsWith('@@')) return 'text-blue-300';
  return 'text-dark-text-secondary';
}

/** The system-prompt note that tells the model where these tools work. */
export function developerSystemPrompt(project: string): string {
  const where = project ? `the project "${project}" (/workspace/${project})` : 'the whole space (/workspace)';
  return 'You have Developer Space tools (dev_*). They work in the user\'s own persistent Linux container, ' +
    `currently on ${where}; pass project to address another one, and use dev_projects to list them. ` +
    'Use dev_clone to bring a Git repository in, dev_glob/dev_search/dev_read_file to read it, dev_edit_file/dev_write_file/dev_patch to change it, ' +
    'and dev_git_commit to commit. Commits are made as the user. The user approves each dev_bash command. ' +
    'Pushing is not a tool: when work is ready, tell the user to push it from the Developer Spaces page.';
}
