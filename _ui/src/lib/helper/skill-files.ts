// Pure helpers for presenting files inside a skill folder. Kept free of
// imports so they can be unit-tested without a bundler.

export function isMarkdownPath(path: string): boolean {
  return /\.(md|markdown|mdx)$/i.test(path);
}

const extensionLanguages: Record<string, string> = {
  sh: 'bash', bash: 'bash', zsh: 'bash',
  py: 'python', js: 'javascript', mjs: 'javascript', cjs: 'javascript', ts: 'typescript',
  json: 'json', jsonc: 'json', yaml: 'yaml', yml: 'yaml', toml: 'ini', ini: 'ini', cfg: 'ini', env: 'ini',
  go: 'go', rs: 'rust', rb: 'ruby', php: 'php', java: 'java', kt: 'kotlin', swift: 'swift',
  sql: 'sql', css: 'css', scss: 'scss', html: 'xml', xml: 'xml', svg: 'xml',
  md: 'markdown', markdown: 'markdown', diff: 'diff', patch: 'diff', graphql: 'graphql', gql: 'graphql',
};

/** highlight.js language for a path, or '' when it should render as plain text. */
export function codeLanguage(path: string): string {
  const name = path.split('/').at(-1)?.toLowerCase() ?? '';
  if (name === 'dockerfile') return 'dockerfile';
  if (name === 'makefile') return 'makefile';
  const ext = name.includes('.') ? name.split('.').at(-1)! : '';
  return extensionLanguages[ext] ?? '';
}

export interface Frontmatter {
  /** The YAML between the fences, verbatim. */
  raw: string;
  /** Flat `key: value` pairs, or null when the YAML is nested/multi-line and must be shown raw. */
  fields: [string, string][] | null;
}

/**
 * Split a leading `---` YAML frontmatter block (the SKILL.md header) from
 * the markdown body. Without this, marked renders the block as a rule plus
 * a setext heading, which turns `description:` into a giant title.
 */
export function splitFrontmatter(source: string): { frontmatter: Frontmatter | null; body: string } {
  const match = source.match(/^﻿?---[ \t]*\r?\n([\s\S]*?)\r?\n(?:---|\.\.\.)[ \t]*(?:\r?\n|$)/);
  if (!match) return { frontmatter: null, body: source };
  const raw = match[1];
  const fields: [string, string][] = [];
  let flat = true;
  for (const line of raw.split(/\r?\n/)) {
    if (!line.trim() || /^\s*#/.test(line)) continue;
    const pair = line.match(/^([A-Za-z0-9_.-]+):[ \t]*(.*)$/);
    // Indented lines, lists and block scalars (|, >) are structure a flat
    // key/value table would misrepresent.
    if (!pair || /^[|>][+-]?\d*$/.test(pair[2].trim()) || pair[2].trim() === '') { flat = false; break; }
    fields.push([pair[1], unquote(pair[2].trim())]);
  }
  return { frontmatter: { raw, fields: flat && fields.length ? fields : null }, body: source.slice(match[0].length) };
}

function unquote(value: string): string {
  if (value.length >= 2 && ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'")))) {
    return value.slice(1, -1);
  }
  return value;
}

/**
 * Resolve a markdown link to a path inside the skill folder, relative to the
 * file containing it (a leading `/` means the skill root). Returns null for
 * external URLs, in-page anchors and anything escaping the folder.
 */
export function resolveSkillLink(href: string, currentPath: string): string | null {
  if (!href || href.startsWith('#') || href.startsWith('//') || /^[a-z][a-z0-9+.-]*:/i.test(href)) return null;
  let target = href.split(/[?#]/)[0];
  try { target = decodeURIComponent(target); } catch { return null; }
  if (!target) return null;
  const base = target.startsWith('/') ? [] : currentPath.split('/').slice(0, -1);
  const parts = [...base];
  for (const segment of target.split('/')) {
    if (segment === '' || segment === '.') continue;
    if (segment === '..') {
      if (!parts.length) return null;
      parts.pop();
      continue;
    }
    parts.push(segment);
  }
  return parts.length ? parts.join('/') : null;
}
