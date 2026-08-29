// Studio helpers: typed access to the persistent asset-library manifests
// (character bible, voices, series/episodes) through the generic files API.
// State lives on disk as JSON manifests written by skill handlers; the UI
// reads the same files and writes metadata-only changes directly.

import { browseFiles, fetchFileText, uploadFile, type FileEntry } from './files';

// ─── Types (mirror the manifest schemas) ───

export interface CharacterManifest {
  name: string;
  slug: string;
  file: string;
  description?: string;
  source_url?: string;
  created_at?: string;
  // v2 character-bible fields (all optional; v1 manifests stay valid)
  version?: number;
  updated_at?: string;
  turnarounds?: string[];
  reference_images?: string[];
  voice?: string;
  sora_character_id?: string;
  sora_character_name?: string;
  lora_url?: string;
  style_notes?: string;
  wardrobe?: string;
  persona?: string;
}

export interface CharacterItem {
  manifest: CharacterManifest;
  imagePath: string;
  manifestPath: string;
  modTime: string;
}

export interface VoiceItem {
  name: string;
  slug: string;
  voice_id: string;
}

export interface SeriesStyle {
  block?: string;
  negative?: string;
  seed?: number | null;
  aspect_ratio?: string;
  draft_model?: string;
  final_model?: string;
}

export interface SeriesCastEntry {
  character: string;
  role?: string;
}

export interface SeriesManifest {
  name: string;
  slug: string;
  description?: string;
  cast: SeriesCastEntry[];
  style: SeriesStyle;
  created_at?: string;
  updated_at?: string;
}

export interface ShotDialogue {
  character: string;
  line: string;
}

export interface Shot {
  id: string;
  scene?: number;
  duration_s?: number;
  description?: string;
  dialogue?: ShotDialogue[];
  prompt?: string;
  references?: string[];
  model?: string;
  seed?: number | null;
  still?: string;
  clip?: string;
  first_frame?: string;
  last_frame?: string;
  status?: string;
  cost_estimate_usd?: number | null;
  updated_at?: string;
}

export interface EpisodeManifest {
  number: number;
  title?: string;
  synopsis?: string;
  status?: string;
  task_id?: string;
  script?: string;
  shots: Shot[];
  final_video?: string;
  duration_s?: number;
  created_at?: string;
  updated_at?: string;
}

export interface EpisodeItem {
  manifest: EpisodeManifest;
  dir: string; // absolute episode directory
  manifestPath: string;
}

const IMG_EXT = ['.png', '.jpg', '.jpeg', '.webp'];

// ─── Characters ───

export async function loadCharacters(assetsRoot: string): Promise<CharacterItem[]> {
  if (!assetsRoot) return [];
  let entries: FileEntry[] = [];
  try {
    entries = (await browseFiles(`${assetsRoot}/avatars`)).entries || [];
  } catch {
    return [];
  }
  const files = entries.filter((e) => !e.is_dir);
  const manifests = new Map<string, { manifest: CharacterManifest; path: string }>();
  for (const e of files.filter((x) => x.name.endsWith('.json'))) {
    try {
      const manifest = JSON.parse(await fetchFileText(e.path));
      manifests.set(e.name.replace(/\.json$/, ''), { manifest, path: e.path });
    } catch {
      /* broken manifest — surfaced as manifest-less image below */
    }
  }
  const items: CharacterItem[] = [];
  const claimed = new Set<string>();
  for (const [stem, m] of manifests) {
    const img =
      files.find((e) => e.name === basename(m.manifest.file || '')) ||
      files.find((e) => IMG_EXT.some((ext) => e.name === stem + ext));
    if (img) claimed.add(img.name);
    // Turnaround images belong to their character, not the gallery grid.
    for (const t of m.manifest.turnarounds || []) claimed.add(basename(t));
    items.push({
      manifest: { ...m.manifest, slug: m.manifest.slug || stem },
      imagePath: img?.path || '',
      manifestPath: m.path,
      modTime: img?.mod_time || '',
    });
  }
  // Images without a manifest still count as (v0) characters.
  for (const e of files) {
    if (claimed.has(e.name) || !IMG_EXT.some((ext) => e.name.toLowerCase().endsWith(ext))) continue;
    const stem = e.name.replace(/\.[^.]+$/, '');
    if (manifests.has(stem)) continue;
    items.push({
      manifest: { name: stem, slug: stem, file: e.path },
      imagePath: e.path,
      manifestPath: `${assetsRoot}/avatars/${stem}.json`,
      modTime: e.mod_time,
    });
  }
  return items.sort((a, b) => a.manifest.name.localeCompare(b.manifest.name));
}

/** Write a character manifest directly (metadata-only edits: voice binding, notes). */
export async function saveCharacterManifest(assetsRoot: string, manifest: CharacterManifest): Promise<void> {
  const slug = manifest.slug;
  const updated: CharacterManifest = {
    ...manifest,
    version: 2,
    updated_at: new Date().toISOString().replace(/\.\d+Z$/, 'Z'),
  };
  const blob = new File([JSON.stringify(updated, null, 1)], `${slug}.json`, { type: 'application/json' });
  await uploadFile(blob, `${assetsRoot}/avatars`, `${slug}.json`);
}

/** Absolute path of a manifest-referenced image (basename or already absolute). */
export function characterAssetPath(assetsRoot: string, rel: string): string {
  if (!rel) return '';
  if (rel.startsWith('/')) return rel;
  return `${assetsRoot}/avatars/${rel}`;
}

// ─── Voices ───

export async function loadVoices(assetsRoot: string): Promise<VoiceItem[]> {
  if (!assetsRoot) return [];
  try {
    const res = await browseFiles(`${assetsRoot}/voices`);
    const out: VoiceItem[] = [];
    for (const e of (res.entries || []).filter((x) => !x.is_dir && x.name.endsWith('.json'))) {
      try {
        const m = JSON.parse(await fetchFileText(e.path));
        if (m.voice_id) {
          out.push({
            name: m.name || e.name.replace(/\.json$/, ''),
            slug: m.slug || e.name.replace(/\.json$/, ''),
            voice_id: m.voice_id,
          });
        }
      } catch {
        /* ignore */
      }
    }
    return out;
  } catch {
    return [];
  }
}

// ─── Series / Episodes ───

export async function loadSeriesList(assetsRoot: string): Promise<SeriesManifest[]> {
  if (!assetsRoot) return [];
  let dirs: FileEntry[] = [];
  try {
    dirs = ((await browseFiles(`${assetsRoot}/series`)).entries || []).filter((e) => e.is_dir);
  } catch {
    return [];
  }
  const out: SeriesManifest[] = [];
  for (const d of dirs) {
    try {
      const m = JSON.parse(await fetchFileText(`${d.path}/series.json`));
      if (m.slug) out.push({ cast: [], style: {}, ...m });
    } catch {
      /* not a series dir */
    }
  }
  return out.sort((a, b) => a.name.localeCompare(b.name));
}

export async function loadSeries(assetsRoot: string, slug: string): Promise<SeriesManifest | null> {
  try {
    const m = JSON.parse(await fetchFileText(`${assetsRoot}/series/${slug}/series.json`));
    return { cast: [], style: {}, ...m };
  } catch {
    return null;
  }
}

export async function saveSeriesManifest(assetsRoot: string, manifest: SeriesManifest): Promise<void> {
  const updated = {
    ...manifest,
    created_at: manifest.created_at || new Date().toISOString().replace(/\.\d+Z$/, 'Z'),
    updated_at: new Date().toISOString().replace(/\.\d+Z$/, 'Z'),
  };
  const blob = new File([JSON.stringify(updated, null, 1)], 'series.json', { type: 'application/json' });
  await uploadFile(blob, `${assetsRoot}/series/${manifest.slug}`, 'series.json');
}

export async function loadEpisodes(assetsRoot: string, slug: string): Promise<EpisodeItem[]> {
  let dirs: FileEntry[] = [];
  try {
    dirs = ((await browseFiles(`${assetsRoot}/series/${slug}/episodes`)).entries || []).filter((e) => e.is_dir);
  } catch {
    return [];
  }
  const out: EpisodeItem[] = [];
  for (const d of dirs) {
    try {
      const m = JSON.parse(await fetchFileText(`${d.path}/episode.json`));
      out.push({ manifest: { shots: [], ...m }, dir: d.path, manifestPath: `${d.path}/episode.json` });
    } catch {
      /* skip */
    }
  }
  return out.sort((a, b) => (a.manifest.number || 0) - (b.manifest.number || 0));
}

export async function loadEpisode(assetsRoot: string, slug: string, num: number): Promise<EpisodeItem | null> {
  const dir = `${assetsRoot}/series/${slug}/episodes/${pad2(num)}`;
  try {
    const m = JSON.parse(await fetchFileText(`${dir}/episode.json`));
    return { manifest: { shots: [], ...m }, dir, manifestPath: `${dir}/episode.json` };
  } catch {
    return null;
  }
}

export async function saveEpisodeManifest(dir: string, manifest: EpisodeManifest): Promise<void> {
  const updated: EpisodeManifest = {
    ...manifest,
    updated_at: new Date().toISOString().replace(/\.\d+Z$/, 'Z'),
  };
  const blob = new File([JSON.stringify(updated, null, 1)], 'episode.json', { type: 'application/json' });
  await uploadFile(blob, dir, 'episode.json');
}

/** Scaffold an episode before submitting its org task, so task_id can be bound immediately. */
export async function createEpisodeDraft(
  assetsRoot: string,
  slug: string,
  existing: EpisodeItem[],
  title: string,
  synopsis: string,
): Promise<EpisodeItem> {
  const num = existing.length ? Math.max(...existing.map((e) => e.manifest.number || 0)) + 1 : 1;
  const dir = `${assetsRoot}/series/${slug}/episodes/${pad2(num)}`;
  const now = new Date().toISOString().replace(/\.\d+Z$/, 'Z');
  const manifest: EpisodeManifest = {
    number: num,
    title,
    synopsis,
    status: 'draft',
    task_id: '',
    script: '',
    shots: [],
    final_video: '',
    duration_s: 0,
    created_at: now,
    updated_at: now,
  };
  await saveEpisodeManifest(dir, manifest);
  return { manifest, dir, manifestPath: `${dir}/episode.json` };
}

/** Patch the episode manifest's task_id (read-modify-write, UI-side cross-link). */
export async function bindEpisodeTask(assetsRoot: string, slug: string, num: number, taskId: string): Promise<void> {
  const ep = await loadEpisode(assetsRoot, slug, num);
  if (!ep) return;
  ep.manifest.task_id = taskId;
  await saveEpisodeManifest(ep.dir, ep.manifest);
}

/** Absolute path of an episode-relative artifact (still/clip/final_video). */
export function episodeAssetPath(dir: string, rel?: string): string {
  if (!rel) return '';
  if (rel.startsWith('/')) return rel;
  return `${dir}/${rel}`;
}

// ─── Misc ───

export function slugify(name: string): string {
  return name
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, ' ')
    .trim()
    .replace(/\s+/g, '-');
}

export function pad2(n: number): string {
  return String(n).padStart(2, '0');
}

function basename(p: string): string {
  const i = p.lastIndexOf('/');
  return i >= 0 ? p.slice(i + 1) : p;
}

export const SHOT_STATUS_COLORS: Record<string, string> = {
  planned: 'bg-gray-100 text-gray-600 dark:bg-dark-elevated dark:text-dark-text-muted',
  still_ready: 'bg-blue-50 text-blue-600 dark:bg-blue-950 dark:text-blue-300',
  generated: 'bg-amber-50 text-amber-700 dark:bg-amber-950 dark:text-amber-300',
  approved: 'bg-green-50 text-green-700 dark:bg-green-950 dark:text-green-300',
  failed: 'bg-red-50 text-red-600 dark:bg-red-950 dark:text-red-300',
};

export const EPISODE_STATUS_COLORS: Record<string, string> = {
  draft: 'text-gray-500 dark:text-dark-text-muted',
  scripted: 'text-blue-600 dark:text-blue-400',
  generating: 'text-amber-600 dark:text-amber-400',
  assembled: 'text-green-600 dark:text-green-400',
  published: 'text-green-700 dark:text-green-300',
};
