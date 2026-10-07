/**
 * Chats slash commands: parsing the composer, expanding custom templates and
 * the compaction boundary. Pure helpers, so the page owns state and these stay
 * testable without a DOM.
 */

export interface SlashInput {
  /** Lowercased command name without the slash. */
  name: string;
  /** Everything after the name, trimmed. */
  args: string;
}

/**
 * Reads `/name args` from the composer. Only a slash at the very start counts,
 * and a second slash in the name means a path (`/usr/bin`), not a command.
 */
export function parseSlashInput(text: string): SlashInput | null {
  const match = /^\/([a-zA-Z0-9][a-zA-Z0-9._-]{0,31})(?:\s+([\s\S]*))?$/.exec(text.trim());
  if (!match) return null;
  return { name: match[1].toLowerCase(), args: (match[2] ?? '').trim() };
}

/**
 * The partial command being typed, for the suggestion list: `/` or `/re`
 * while the composer holds nothing else. `null` once a space or newline
 * follows, because then the reader is writing arguments.
 */
export function slashQuery(text: string): string | null {
  const match = /^\/([a-zA-Z0-9._-]{0,32})$/.exec(text);
  return match ? match[1].toLowerCase() : null;
}

/** Splits arguments like a shell: whitespace, with "double" or 'single' quotes. */
export function splitArgs(args: string): string[] {
  const out: string[] = [];
  const re = /"([^"]*)"|'([^']*)'|(\S+)/g;
  let m: RegExpExecArray | null;
  while ((m = re.exec(args))) out.push(m[1] ?? m[2] ?? m[3]);
  return out;
}

/**
 * Expands a command template: `$ARGUMENTS` is the whole argument string and
 * `$1`..`$9` are positional arguments (missing ones become empty). A template
 * that mentions neither gets the arguments appended on a new paragraph, so
 * `/review some notes` still delivers the notes.
 */
export function expandCommandTemplate(template: string, args: string): string {
  const positional = splitArgs(args);
  let used = false;
  const out = template.replace(/\$(ARGUMENTS|[1-9])/g, (_, key: string) => {
    used = true;
    return key === 'ARGUMENTS' ? args : (positional[Number(key) - 1] ?? '');
  });
  if (!used && args) return `${out.trimEnd()}\n\n${args}`;
  return out;
}

/** Marker stored on a compaction summary's message data. */
export const COMPACTION_FLAG = 'compaction';

export const COMPACTION_PROMPT = `Summarize the conversation so far so it can continue in a fresh context. The summary replaces the earlier messages: anything you leave out is lost.

Include, in this order:
1. The user's goal and any constraints or preferences they stated.
2. Decisions made and the reasons for them.
3. Work completed: files, commands, tool results and facts established, with exact names, paths, identifiers and values.
4. Open questions, errors not yet resolved and the next steps.

Write it as plain notes addressed to yourself. Do not call tools. Do not answer the conversation; only summarize it.`;

/** Text a compaction summary is stored with, so the model reads it as context. */
export function compactionMessageText(summary: string, instructions = ''): string {
  const focus = instructions ? `\n\nThe user asked the summary to focus on: ${instructions}` : '';
  return `[Summary of the earlier conversation, written to continue it in a fresh context.${focus}]\n\n${summary.trim()}`;
}

export function compactionRequest(instructions: string): string {
  return instructions ? `${COMPACTION_PROMPT}\n\nFocus the summary on: ${instructions}` : COMPACTION_PROMPT;
}

/**
 * Index of the latest compaction summary, or -1. Messages before it are
 * history the reader can still scroll, but they no longer go to the model.
 */
export function compactionStart(flags: ReadonlyArray<boolean | undefined>): number {
  for (let i = flags.length - 1; i >= 0; i--) if (flags[i]) return i;
  return -1;
}
