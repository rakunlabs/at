export interface SwitchPort { id: string; label: string }

export function switchOutputPorts(data: Record<string, any>): SwitchPort[] {
  const ports: SwitchPort[] = [];
  const seen = new Set<string>();
  for (const rule of Array.isArray(data.rules) ? data.rules : []) {
    if (typeof rule?.id !== 'string' || !/^case_[A-Za-z0-9_-]{1,59}$/.test(rule.id) || seen.has(rule.id)) continue;
    seen.add(rule.id);
    ports.push({ id: rule.id, label: typeof rule.label === 'string' && rule.label ? rule.label : rule.id });
  }
  return [...ports, { id: 'fallback', label: 'Fallback' }];
}

let caseSequence = 0;
export function newSwitchRule(existing: Record<string, any>[] = []) {
  let id: string;
  do { id = `case_${Date.now().toString(36)}_${++caseSequence}`; } while (existing.some(rule => rule?.id === id));
  return { id, label: `Case ${existing.length + 1}`, ...newDataCondition() };
}

export function newDataCondition() {
  return { path: '', operator: 'exists', value_type: 'string', value: '' };
}

export function literalError(config: Record<string, any>): string {
  const kind = config.value_type || 'string';
  if (!['string', 'number', 'boolean', 'null', 'json'].includes(kind)) return 'Choose a supported value type.';
  if (kind === 'string' || kind === 'null') return '';
  try {
    const value = JSON.parse(config.value ?? '');
    const pending = [value];
    while (pending.length) {
      const item = pending.pop();
      if (typeof item === 'number' && !Number.isFinite(item)) return 'Numbers must be finite.';
      if (item && typeof item === 'object') pending.push(...Object.values(item));
    }
    if (kind === 'number' && (typeof value !== 'number' || !Number.isFinite(value))) return 'Enter a finite JSON number.';
    if (kind === 'boolean' && typeof value !== 'boolean') return 'Select true or false.';
    return '';
  } catch { return `Enter a valid ${kind === 'json' ? 'JSON value' : kind}.`; }
}
