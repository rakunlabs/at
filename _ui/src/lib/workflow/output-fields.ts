// Named Output fields: each becomes an input port of the Output node and a
// top-level workflow output, and Workflow Call can expose them as separate
// output ports. Mirrors outputFieldNames in internal/service/workflow/nodes.
const fieldName = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;
const reserved = new Set(['input', 'output', 'files']);

export function outputFieldNames(raw: unknown): string[] {
  if (!Array.isArray(raw)) return [];
  const names: string[] = [];
  for (const item of raw) {
    const name = (typeof item === 'string' ? item : typeof item?.name === 'string' ? item.name : '').trim();
    if (!name || names.includes(name) || reserved.has(name) || !fieldName.test(name)) continue;
    names.push(name);
    if (names.length === 32) break;
  }
  return names;
}

// Why a typed name gets no port; '' when it is usable.
export function outputFieldProblem(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) return '';
  if (reserved.has(trimmed)) return `"${trimmed}" is reserved`;
  if (!fieldName.test(trimmed)) return 'Use letters, digits and _ (not starting with a digit)';
  return '';
}

// The Output node of a workflow graph that defines its result fields: the
// first Output node with named fields, else none.
export function workflowOutputFields(graph: { nodes?: { type: string; data?: Record<string, any> }[] } | undefined): string[] {
  for (const node of graph?.nodes ?? []) {
    if (node.type !== 'output') continue;
    const names = outputFieldNames(node.data?.fields);
    if (names.length) return names;
  }
  return [];
}
