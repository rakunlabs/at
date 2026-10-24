import type { ElicitProperty, ElicitSchema } from '../api/mcp-elicitations';

/** One form field built from an elicitation schema property. */
export interface ElicitField {
  name: string;
  label: string;
  description: string;
  kind: 'text' | 'number' | 'boolean' | 'select';
  required: boolean;
  options: Array<{ value: string; label: string }>;
  inputType: string;
  property: ElicitProperty;
}

export function elicitFields(schema: ElicitSchema): ElicitField[] {
  const required = new Set(schema.required ?? []);
  return Object.entries(schema.properties ?? {}).map(([name, property]) => {
    const enumValues = property.type === 'string' && Array.isArray(property.enum) ? property.enum : null;
    const kind: ElicitField['kind'] = enumValues ? 'select'
      : property.type === 'boolean' ? 'boolean'
        : property.type === 'number' || property.type === 'integer' ? 'number' : 'text';
    const inputType = kind === 'number' ? 'number'
      : property.format === 'email' ? 'email'
        : property.format === 'uri' ? 'url'
          : property.format === 'date' ? 'date'
            : property.format === 'date-time' ? 'datetime-local' : 'text';
    return {
      name, property, kind, inputType,
      label: property.title || name,
      description: property.description || '',
      required: required.has(name),
      options: (enumValues ?? []).map((value, i) => ({ value, label: property.enumNames?.[i] ?? value })),
    };
  });
}

/** Initial values: schema defaults, else empty (booleans false). */
export function elicitDefaults(fields: ElicitField[]): Record<string, string | boolean> {
  const values: Record<string, string | boolean> = {};
  for (const f of fields) {
    const d = f.property.default;
    if (f.kind === 'boolean') values[f.name] = d === true;
    else values[f.name] = d === undefined || d === null ? '' : String(d);
  }
  return values;
}

/**
 * Turns form values into the content of an accept answer, or reports the
 * first problem. Empty optional fields are left out.
 */
export function elicitContent(fields: ElicitField[], values: Record<string, string | boolean>): { content: Record<string, unknown> } | { error: string } {
  const content: Record<string, unknown> = {};
  for (const f of fields) {
    const raw = values[f.name];
    if (f.kind === 'boolean') { content[f.name] = raw === true; continue; }
    const text = typeof raw === 'string' ? raw.trim() : '';
    if (!text) {
      if (f.required) return { error: `${f.label} is required.` };
      continue;
    }
    if (f.kind === 'number') {
      const n = Number(text);
      if (!Number.isFinite(n)) return { error: `${f.label} must be a number.` };
      if (f.property.type === 'integer' && !Number.isInteger(n)) return { error: `${f.label} must be a whole number.` };
      if (f.property.minimum !== undefined && n < f.property.minimum) return { error: `${f.label} must be at least ${f.property.minimum}.` };
      if (f.property.maximum !== undefined && n > f.property.maximum) return { error: `${f.label} must be at most ${f.property.maximum}.` };
      content[f.name] = n;
      continue;
    }
    if (f.property.minLength !== undefined && text.length < f.property.minLength) return { error: `${f.label} needs at least ${f.property.minLength} characters.` };
    if (f.property.maxLength !== undefined && text.length > f.property.maxLength) return { error: `${f.label} allows at most ${f.property.maxLength} characters.` };
    content[f.name] = text;
  }
  return { content };
}

/**
 * Polls for questions while `active()` holds: a long-poll that returns as
 * soon as a question is open, then a short pause. Questions are delivered to
 * onChange; stop() ends the loop.
 */
export function watchElicitations<T>(list: (wait: number, signal: AbortSignal) => Promise<T[]>, onChange: (items: T[]) => void) {
  let controller: AbortController | null = null;
  let running = false;
  const loop = async () => {
    while (running && controller) {
      const signal = controller.signal;
      try {
        const items = await list(20, signal);
        if (!running || signal.aborted) return;
        onChange(items);
        // While a question is open, refresh slowly; otherwise long-poll again.
        await new Promise(resolve => setTimeout(resolve, items.length ? 2000 : 250));
      } catch {
        if (!running || signal.aborted) return;
        await new Promise(resolve => setTimeout(resolve, 3000));
      }
    }
  };
  return {
    start() {
      if (running) return;
      running = true;
      controller = new AbortController();
      void loop();
    },
    stop() {
      running = false;
      controller?.abort();
      controller = null;
      onChange([]);
    },
  };
}

/**
 * Splits a URL around its host so the page can highlight the domain, which
 * is what tells a person whose site they are about to open.
 */
export function urlParts(raw: string): { before: string; host: string; after: string } | null {
  let u: URL;
  try { u = new URL(raw); } catch { return null; }
  if (u.protocol !== 'https:' && u.protocol !== 'http:') return null;
  const at = raw.indexOf(u.host);
  if (at < 0) return { before: `${u.protocol}//`, host: u.host, after: raw.slice(`${u.protocol}//`.length + u.host.length) };
  return { before: raw.slice(0, at), host: u.host, after: raw.slice(at + u.host.length) };
}

/** Opens an elicitation URL in a separate tab that cannot reach this page. */
export function openElicitationURL(raw: string): boolean {
  if (!urlParts(raw)) return false;
  const tab = window.open(raw, '_blank', 'noopener,noreferrer');
  return tab !== undefined;
}
