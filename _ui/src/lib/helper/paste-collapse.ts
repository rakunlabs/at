// Large pastes are shown in the composer as a short placeholder such as
// `[Pasted ~30 lines]`, removed with a single Backspace and expanded back to
// the pasted text when the message is sent.

export const PASTE_MIN_LINES = 3;
export const PASTE_MIN_CHARS = 400;

const PLACEHOLDER = /\[Pasted ~\d+ (?:lines|chars)(?: #\d+)?\]/g;

export function shouldCollapsePaste(text: string): boolean {
  return lineCount(text) >= PASTE_MIN_LINES || text.length >= PASTE_MIN_CHARS;
}

function lineCount(text: string): number {
  return text.replace(/\r\n?/g, '\n').replace(/\n+$/, '').split('\n').length;
}

/** A placeholder not yet used by entries. */
export function pastePlaceholder(text: string, entries: ReadonlyMap<string, string>): string {
  const lines = lineCount(text);
  const base = lines >= PASTE_MIN_LINES ? `Pasted ~${lines} lines` : `Pasted ~${text.length} chars`;
  let label = `[${base}]`;
  for (let n = 2; entries.has(label); n++) label = `[${base} #${n}]`;
  return label;
}

/** Placeholders present in value that have stored text, in order. */
export function pasteRanges(value: string, entries: ReadonlyMap<string, string>): { start: number; end: number; label: string }[] {
  const ranges: { start: number; end: number; label: string }[] = [];
  for (const match of value.matchAll(PLACEHOLDER)) {
    if (entries.has(match[0])) ranges.push({ start: match.index!, end: match.index! + match[0].length, label: match[0] });
  }
  return ranges;
}

/** Replaces every stored placeholder with the text it stands for. */
export function expandPastes(value: string, entries: ReadonlyMap<string, string>): string {
  if (entries.size === 0) return value;
  return value.replace(PLACEHOLDER, label => entries.get(label) ?? label);
}

export interface ComposerEdit { value: string; caret: number }

export function insertPaste(value: string, start: number, end: number, text: string, entries: Map<string, string>): ComposerEdit {
  const label = pastePlaceholder(text, entries);
  entries.set(label, text);
  return { value: value.slice(0, start) + label + value.slice(end), caret: start + label.length };
}

/**
 * Backspace just after (or inside) a placeholder, or Delete just before it,
 * removes the whole placeholder. Returns null when the key edits as usual.
 */
export function deletePaste(value: string, start: number, end: number, key: string, entries: Map<string, string>): ComposerEdit | null {
  if (start !== end || (key !== 'Backspace' && key !== 'Delete')) return null;
  const hit = pasteRanges(value, entries).find(r => key === 'Backspace' ? start > r.start && start <= r.end : start >= r.start && start < r.end);
  if (!hit) return null;
  entries.delete(hit.label);
  return { value: value.slice(0, hit.start) + value.slice(hit.end), caret: hit.start };
}

/** Drops stored text whose placeholder is no longer in the composer. */
export function prunePastes(value: string, entries: Map<string, string>): void {
  for (const label of [...entries.keys()]) if (!value.includes(label)) entries.delete(label);
}

/**
 * Per-composer state. Handlers update the textarea and return its new value
 * for the component's bound variable, or null when the event was not handled.
 */
export class PastedText {
  readonly entries = new Map<string, string>();

  paste(event: ClipboardEvent, el: HTMLTextAreaElement): string | null {
    const text = event.clipboardData?.getData('text/plain') ?? '';
    if (!text || (event.clipboardData?.files?.length ?? 0) > 0 || !shouldCollapsePaste(text)) return null;
    event.preventDefault();
    prunePastes(el.value, this.entries);
    return this.apply(el, insertPaste(el.value, el.selectionStart, el.selectionEnd, text, this.entries));
  }

  keydown(event: KeyboardEvent, el: HTMLTextAreaElement): string | null {
    if (event.isComposing || event.ctrlKey || event.metaKey || event.altKey || this.entries.size === 0) return null;
    const edit = deletePaste(el.value, el.selectionStart, el.selectionEnd, event.key, this.entries);
    if (!edit) return null;
    event.preventDefault();
    return this.apply(el, edit);
  }

  /** The text to send. Call clear() once the composer is emptied. */
  expand(value: string): string {
    return expandPastes(value, this.entries);
  }

  clear(): void {
    this.entries.clear();
  }

  private apply(el: HTMLTextAreaElement, edit: ComposerEdit): string {
    el.value = edit.value;
    el.setSelectionRange(edit.caret, edit.caret);
    el.dispatchEvent(new Event('input', { bubbles: true }));
    return edit.value;
  }
}
