// Pure navigation logic for the /docs page.
//
// Kept free of Svelte and DOM APIs so the URL scheme, the search matcher, the
// persisted group state and the reading order can be unit-tested from
// tests/*.test.mjs without a browser. The components in lib/components/docs/
// own the rendering; this file owns the rules.

/** URL-level kind: a reference section (`?section=`) or a guide (`?guide=`). */
export type DocsKind = 'api' | 'guides';

export interface DocsSelection {
  kind: DocsKind;
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

/** One collapsible sidebar group. `kind` decides which URL key its entries use. */
export interface DocsNavGroup {
  id: string;
  kind: DocsKind;
  entries: DocsSearchable[];
}

export const DOCS_GROUP_STORAGE_KEY = 'at.docs.groups';

/** Group id → expanded. A missing key means expanded. */
export type DocsGroupState = Record<string, boolean>;

/**
 * Old section ids that were merged into another page. Links to them keep
 * working and land on the page that now carries their content.
 */
export const DOCS_SECTION_ALIASES: Record<string, string> = {
  'code-examples': 'quickstart',
  'list-models': 'available-models',
};

export function resolveSectionAlias(id: string): string {
  return DOCS_SECTION_ALIASES[id] ?? id;
}

/**
 * Parse the `/docs` query string into a selection.
 *
 * Supported (current):
 *   ?section=<section-id>       → a reference section
 *   ?guide=<guide-id>           → a guide
 *
 * Supported (legacy, emitted by the previous two-tab page):
 *   ?g=<guide-id>               → a guide
 *   ?section=guides             → the guides group, first entry
 *   ?section=guides&g=<id>      → a guide
 *
 * Returns null when the query selects nothing, so the caller can fall back to
 * its own default (the overview).
 */
export function parseDocsQuery(queryString: string): DocsSelection | null {
  const qs = new URLSearchParams(queryString || '');

  const guide = qs.get('guide') || qs.get('g');
  if (guide) return { kind: 'guides', id: guide };

  const section = qs.get('section');
  if (!section) return null;
  // Legacy tab name — no specific guide, so let the caller pick the first one.
  if (section === 'guides') return { kind: 'guides', id: '' };
  return { kind: 'api', id: resolveSectionAlias(section) };
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
 * Case-insensitive match over title, description and body. Every
 * whitespace-separated term must appear somewhere, so "stream fallback" finds
 * the page that mentions both words in different places. An empty query
 * matches everything so callers can use one code path.
 */
export function matchesQuery(entry: DocsSearchable, query: string): boolean {
  const terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (terms.length === 0) return true;
  const haystack = `${entry.title}\n${entry.description}\n${entry.body ?? ''}`.toLowerCase();
  return terms.every((t) => haystack.includes(t));
}

export function filterEntries<T extends DocsSearchable>(entries: T[], query: string): T[] {
  const q = query.trim();
  if (!q) return entries;
  return entries.filter((e) => matchesQuery(e, q));
}

/**
 * Read the persisted expand/collapse state. Only boolean values survive;
 * anything unparseable yields `{}` ("everything open") — a corrupted key must
 * never hide the navigation.
 */
export function parseGroupState(raw: string | null | undefined): DocsGroupState {
  if (!raw) return {};
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) return {};
    const out: DocsGroupState = {};
    for (const [k, v] of Object.entries(parsed as Record<string, unknown>)) {
      if (typeof v === 'boolean') out[k] = v;
    }
    return out;
  } catch {
    return {};
  }
}

export function serializeGroupState(state: DocsGroupState): string {
  return JSON.stringify(state);
}

export function groupExpanded(state: DocsGroupState, id: string): boolean {
  return state[id] !== false;
}

/**
 * Flatten the sidebar into the row order the arrow keys walk. Group headers are
 * always present; their children only when the group is expanded. The `key` is
 * what the tree uses for roving focus and DOM lookup.
 */
export interface DocsNavRow {
  key: string;
  kind: 'group' | 'entry';
  group: string;
  entryKind: DocsKind;
  id: string;
}

export function navRowKey(kind: DocsKind, id: string): string {
  return `${kind}:${id}`;
}

export function groupRowKey(group: string): string {
  return `group:${group}`;
}

export function buildNavRows(groups: DocsNavGroup[], state: DocsGroupState): DocsNavRow[] {
  const rows: DocsNavRow[] = [];
  for (const g of groups) {
    rows.push({ key: groupRowKey(g.id), kind: 'group', group: g.id, entryKind: g.kind, id: '' });
    if (!groupExpanded(state, g.id)) continue;
    for (const e of g.entries) {
      rows.push({
        key: navRowKey(g.kind, e.id),
        kind: 'entry',
        group: g.id,
        entryKind: g.kind,
        id: e.id,
      });
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

/** Previous and next entries of a reading order, for the page footer. */
export function adjacentEntries<T extends { id: string }>(
  order: T[],
  id: string,
): { prev: T | null; next: T | null } {
  const i = order.findIndex((e) => e.id === id);
  if (i < 0) return { prev: null, next: null };
  return { prev: order[i - 1] ?? null, next: order[i + 1] ?? null };
}
