// Studio toolkit: which configured tool sources provide each production
// capability. Stored as <assetsRoot>/studio.json; agents read it through the
// studio_library `studio_toolkit` tool and the Studio attaches the sources to
// every production agent.

export const STUDIO_CAPABILITIES = ['image', 'video', 'speech', 'lipsync', 'stock'] as const;
export type StudioCapability = (typeof STUDIO_CAPABILITIES)[number];

export const CAPABILITY_INFO: Record<StudioCapability, { label: string; hint: string }> = {
  image: { label: 'Image', hint: 'Portraits, reference sheets and shot keyframes. Tools that accept reference images keep characters consistent.' },
  video: { label: 'Video', hint: 'Shot clips: text-to-video, image-to-video or reference-to-video.' },
  speech: { label: 'Speech', hint: 'Narration and dialogue. Voice cloning is optional.' },
  lipsync: { label: 'Lip-sync', hint: 'Talking-head videos from a portrait and an audio clip.' },
  stock: { label: 'Stock media', hint: 'Stock photos and B-roll for long videos.' },
};

export interface CapabilitySources {
  mcp_sets: string[];
  skills: string[];
  builtin_tools: string[];
  notes: string;
}

export interface StudioToolkit {
  version: number;
  capabilities: Record<StudioCapability, CapabilitySources>;
  notes: string;
  /** Sources last attached to studio agents, so removed ones can be detached. */
  applied: { mcp_sets: string[]; skills: string[]; builtin_tools: string[] };
  updated_at?: string;
}

/** The skill every Studio production agent carries; it marks agents that receive the toolkit. */
export const STUDIO_LIBRARY_SKILL = 'studio_library';

type SourceKey = 'mcp_sets' | 'skills' | 'builtin_tools';
const SOURCE_KEYS: SourceKey[] = ['mcp_sets', 'skills', 'builtin_tools'];

function names(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  const out: string[] = [];
  for (const item of value) {
    const name = typeof item === 'string' ? item.trim() : '';
    if (name && !out.includes(name)) out.push(name);
  }
  return out;
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

export function emptyCapability(): CapabilitySources {
  return { mcp_sets: [], skills: [], builtin_tools: [], notes: '' };
}

export function createStudioToolkit(): StudioToolkit {
  const capabilities = {} as Record<StudioCapability, CapabilitySources>;
  for (const cap of STUDIO_CAPABILITIES) capabilities[cap] = emptyCapability();
  return { version: 1, capabilities, notes: '', applied: { mcp_sets: [], skills: [], builtin_tools: [] } };
}

/** Accepts any parsed studio.json, keeping only known, well-typed fields. */
export function normalizeStudioToolkit(raw: unknown): StudioToolkit {
  const toolkit = createStudioToolkit();
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return toolkit;
  const data = raw as Record<string, any>;
  const caps = data.capabilities && typeof data.capabilities === 'object' ? data.capabilities : {};
  for (const cap of STUDIO_CAPABILITIES) {
    const entry = caps[cap] && typeof caps[cap] === 'object' ? caps[cap] : {};
    toolkit.capabilities[cap] = {
      mcp_sets: names(entry.mcp_sets),
      skills: names(entry.skills),
      builtin_tools: names(entry.builtin_tools),
      notes: text(entry.notes),
    };
  }
  toolkit.notes = text(data.notes);
  const applied = data.applied && typeof data.applied === 'object' ? data.applied : {};
  for (const key of SOURCE_KEYS) toolkit.applied[key] = names(applied[key]);
  if (typeof data.updated_at === 'string') toolkit.updated_at = data.updated_at;
  return toolkit;
}

export function capabilityConfigured(sources: CapabilitySources): boolean {
  return SOURCE_KEYS.some((key) => sources[key].length > 0);
}

/** Every source selected for any capability, deduplicated per kind. */
export function toolkitSources(toolkit: StudioToolkit): Record<SourceKey, string[]> {
  const out: Record<SourceKey, string[]> = { mcp_sets: [], skills: [], builtin_tools: [] };
  for (const cap of STUDIO_CAPABILITIES) {
    for (const key of SOURCE_KEYS) {
      for (const name of toolkit.capabilities[cap][key]) if (!out[key].includes(name)) out[key].push(name);
    }
  }
  return out;
}

export interface StudioAgentTools {
  skills?: Array<string | { id: string; connections?: Record<string, string> }>;
  mcp_sets?: string[];
  builtin_tools?: string[];
}

function skillID(ref: string | { id: string }): string {
  return typeof ref === 'string' ? ref : ref?.id || '';
}

export function isStudioProductionAgent(config: StudioAgentTools): boolean {
  return (config.skills || []).some((ref) => skillID(ref) === STUDIO_LIBRARY_SKILL);
}

/**
 * Attaches the toolkit's sources to one agent config. Sources the Studio
 * attached earlier but which are no longer selected are removed; anything the
 * agent had on its own stays. Returns null when nothing changes.
 */
export function applyToolkitToAgent<T extends StudioAgentTools>(config: T, previous: StudioToolkit['applied'], next: Record<SourceKey, string[]>): T | null {
  let changed = false;
  const result = { ...config };

  const skills = [...(config.skills || [])];
  const keptSkills = skills.filter((ref) => {
    const id = skillID(ref);
    const drop = id !== STUDIO_LIBRARY_SKILL && previous.skills.includes(id) && !next.skills.includes(id);
    if (drop) changed = true;
    return !drop;
  });
  for (const id of next.skills) {
    if (!keptSkills.some((ref) => skillID(ref) === id)) {
      keptSkills.push(id);
      changed = true;
    }
  }
  result.skills = keptSkills;

  for (const key of ['mcp_sets', 'builtin_tools'] as const) {
    const current = [...(config[key] || [])];
    const kept = current.filter((name) => {
      const drop = previous[key].includes(name) && !next[key].includes(name);
      if (drop) changed = true;
      return !drop;
    });
    for (const name of next[key]) {
      if (!kept.includes(name)) {
        kept.push(name);
        changed = true;
      }
    }
    result[key] = kept;
  }
  return changed ? result : null;
}
