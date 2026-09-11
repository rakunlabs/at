// Pure navigation logic for the /docs page.
//
// Kept free of Svelte and DOM APIs so the URL scheme, the search matcher and
// the persisted group state can be unit-tested from tests/*.test.mjs without a
// browser. The components in lib/components/docs/ own the rendering; this file
// owns the rules.

export type DocsGroupId = 'api' | 'guides';

export interface DocsSelection {
  kind: DocsGroupId;
  /** Section id for `api`, guide id for `guides`. Empty means "first entry". */
  id: string;
}

/** Minimal shape every sidebar entry satisfies, for search + lookup. */
export interface DocsSearchable {
  id: string;
  title: string;
  description: string;
  /** Extra body text folded into the search index. */
  body?: string;
}

export const DOCS_GROUP_STORAGE_KEY = 'at.docs.groups';

export interface DocsGroupState {
  api: boolean;
  guides: boolean;
}

export const DOCS_DEFAULT_GROUP_STATE: DocsGroupState = { api: true, guides: true };

/**
 * Parse the `/docs` query string into a selection.
 *
 * Supported (current):
 *   ?section=<api-section-id>   → an API reference section
 *   ?guide=<guide-id>           → a guide
 *
 * Supported (legacy, emitted by the previous two-tab page):
 *   ?g=<guide-id>               → a guide
 *   ?section=guides             → the guides group, first entry
 *   ?section=guides&g=<id>      → a guide
 *
 * Returns null when the query selects nothing, so the caller can fall back to
 * its own default (the first API section).
 */
export function parseDocsQuery(queryString: string): DocsSelection | null {
  const qs = new URLSearchParams(queryString || '');

  const guide = qs.get('guide') || qs.get('g');
  if (guide) return { kind: 'guides', id: guide };

  const section = qs.get('section');
  if (!section) return null;
  // Legacy tab name — no specific guide, so let the caller pick the first one.
  if (section === 'guides') return { kind: 'guides', id: '' };
  return { kind: 'api', id: section };
}

/** Build the hash-router path for a selection. Inverse of parseDocsQuery. */
export function docsPath(selection: DocsSelection): string {
  const key = selection.kind === 'guides' ? 'guide' : 'section';
  if (!selection.id) return selection.kind === 'guides' ? '/docs?section=guides' : '/docs';
  return `/docs?${key}=${encodeURIComponent(selection.id)}`;
}

/** Absolute, shareable URL for a selection, given the current page location. */
export function docsShareUrl(selection: DocsSelection, origin: string, pathname: string): string {
  return `${origin}${pathname}#${docsPath(selection)}`;
}

export function sameSelection(a: DocsSelection | null, b: DocsSelection | null): boolean {
  if (!a || !b) return a === b;
  return a.kind === b.kind && a.id === b.id;
}

/**
 * Case-insensitive substring match over title, description and body. An empty
 * query matches everything so callers can use one code path.
 */
export function matchesQuery(entry: DocsSearchable, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return (
    entry.title.toLowerCase().includes(q) ||
    entry.description.toLowerCase().includes(q) ||
    (entry.body ?? '').toLowerCase().includes(q)
  );
}

export function filterEntries<T extends DocsSearchable>(entries: T[], query: string): T[] {
  const q = query.trim();
  if (!q) return entries;
  return entries.filter((e) => matchesQuery(e, q));
}

/**
 * Read the persisted expand/collapse state. Anything unparseable falls back to
 * "both groups open" — a corrupted key must never hide the navigation.
 */
export function parseGroupState(raw: string | null | undefined): DocsGroupState {
  if (!raw) return { ...DOCS_DEFAULT_GROUP_STATE };
  try {
    const parsed = JSON.parse(raw) as Partial<Record<DocsGroupId, unknown>>;
    if (!parsed || typeof parsed !== 'object') return { ...DOCS_DEFAULT_GROUP_STATE };
    return {
      api: typeof parsed.api === 'boolean' ? parsed.api : DOCS_DEFAULT_GROUP_STATE.api,
      guides: typeof parsed.guides === 'boolean' ? parsed.guides : DOCS_DEFAULT_GROUP_STATE.guides,
    };
  } catch {
    return { ...DOCS_DEFAULT_GROUP_STATE };
  }
}

export function serializeGroupState(state: DocsGroupState): string {
  return JSON.stringify({ api: state.api, guides: state.guides });
}

/**
 * Flatten the sidebar into the row order the arrow keys walk. Group headers are
 * always present; their children only when the group is expanded and non-empty.
 * The `key` is what the tree uses for roving focus and DOM lookup.
 */
export interface DocsNavRow {
  key: string;
  kind: 'group' | 'entry';
  group: DocsGroupId;
  id: string;
}

export function navRowKey(group: DocsGroupId, id: string): string {
  return `${group}:${id}`;
}

export function buildNavRows(
  apiEntries: DocsSearchable[],
  guideEntries: DocsSearchable[],
  state: DocsGroupState,
): DocsNavRow[] {
  const rows: DocsNavRow[] = [];
  rows.push({ key: 'group:api', kind: 'group', group: 'api', id: '' });
  if (state.api) {
    for (const e of apiEntries) {
      rows.push({ key: navRowKey('api', e.id), kind: 'entry', group: 'api', id: e.id });
    }
  }
  rows.push({ key: 'group:guides', kind: 'group', group: 'guides', id: '' });
  if (state.guides) {
    for (const e of guideEntries) {
      rows.push({ key: navRowKey('guides', e.id), kind: 'entry', group: 'guides', id: e.id });
    }
  }
  return rows;
}

/** Next row index for an arrow key, clamped at both ends. */
export function stepIndex(rows: DocsNavRow[], current: number, delta: number): number {
  if (rows.length === 0) return -1;
  const next = current + delta;
  if (next < 0) return 0;
  if (next > rows.length - 1) return rows.length - 1;
  return next;
}
