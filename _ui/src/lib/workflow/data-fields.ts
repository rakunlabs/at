export interface DataField {
  path: string;
  label: string;
  kind: string;
  preview: string;
  depth: number;
}

export function escapePointerSegment(key: string): string {
  return key.replace(/~/g, '~0').replace(/\//g, '~1');
}

/** Bounded field browser. Selecting a container maps the real container, not
 * the short description shown here. JSON view retains the complete snapshot. */
export function listDataFields(value: unknown, limit = 200): { fields: DataField[]; limited: boolean } {
  const fields: DataField[] = [];
  let limited = false;
  function visit(data: unknown, path: string, depth: number) {
    if (data === null || typeof data !== 'object') return;
    if (depth > 12) { limited = true; return; }
    for (const [key, child] of Object.entries(data)) {
      if (fields.length >= limit) { limited = true; return; }
      const childPath = `${path}/${escapePointerSegment(key)}`;
      const kind = child === null ? 'null' : Array.isArray(child) ? 'array' : typeof child;
      const preview = kind === 'array' ? `${(child as unknown[]).length} items`
        : kind === 'object' ? `${Object.keys(child as object).length} fields`
        : String(child).slice(0, 160);
      fields.push({ path: childPath, label: key || '(empty key)', kind, preview, depth });
      visit(child, childPath, depth + 1);
    }
  }
  visit(value, '', 0);
  return { fields, limited };
}
