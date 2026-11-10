import type { Extension } from '@codemirror/state';

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

/** Top-level folder that held the removed chat's undo snapshots (internal/devfs.SnapshotDir). */
export const SNAPSHOT_DIR = '.at-snapshots';

/** Chats with the Developer Space tools on and this project selected. */
export function chatProjectLink(project: string): string {
  const query = new URLSearchParams({ developer: '1' });
  if (project) query.set('project', project);
  return `#/chats?${query}`;
}

/** The Developer Spaces editor opened at a project file, from Chats. */
export function developerFileLink(project: string, file: string): string {
  const query = new URLSearchParams();
  query.set('project', project);
  query.set('file', joinPath(project, file));
  return `#/developer-spaces?${query}`;
}
