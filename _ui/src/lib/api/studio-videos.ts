import { browseFiles, fetchFileText, uploadFile } from './files';

export interface VideoBrief {
  id: string;
  title: string;
  topic: string;
  content_brief: string;
  audience: string;
  language: string;
  duration_minutes: number;
  aspect_ratio: string;
  visual_style: string;
  outline: string;
}

export interface VideoProject {
  brief: VideoBrief;
  dir: string;
  task_id?: string;
  output?: { status?: string; final_video?: string | null; duration_s?: number | null; updated_at?: string };
}

export const VIDEO_BRIEF_LIMITS = {
  title: 300, topic: 2000, content_brief: 30000, audience: 2000,
  language: 100, aspect_ratio: 4, visual_style: 4000, outline: 30000,
} as const;

function object(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/** Validates the entire patch before returning a new brief; never mutates current.
 * ID is not an editable field, including for assistant-generated updates.
 */
export function applyVideoBriefUpdate(current: VideoBrief, updates: unknown): VideoBrief {
  if (!object(updates)) throw new Error('Brief updates must be an object');
  for (const [key, value] of Object.entries(updates)) {
    if (key === 'duration_minutes') {
      if (typeof value !== 'number' || !Number.isFinite(value) || value < 1 || value > 60) {
        throw new Error('Target duration must be a number between 1 and 60 minutes');
      }
    } else {
      if (!Object.hasOwn(VIDEO_BRIEF_LIMITS, key)) throw new Error(`Unknown or immutable brief field: ${key}`);
      if (typeof value !== 'string') throw new Error(`${key} must be a string`);
      if (value.length > VIDEO_BRIEF_LIMITS[key as keyof typeof VIDEO_BRIEF_LIMITS]) {
        throw new Error(`${key} exceeds the character limit (${VIDEO_BRIEF_LIMITS[key as keyof typeof VIDEO_BRIEF_LIMITS]})`);
      }
      if (key === 'aspect_ratio' && !['16:9', '9:16', '1:1'].includes(value)) {
        throw new Error('Aspect ratio must be 16:9, 9:16, or 1:1');
      }
    }
  }
  return { ...current, ...updates, id: current.id };
}

export function createVideoBrief(): VideoBrief {
  return {
    id: crypto.randomUUID(), title: '', topic: '', content_brief: '', audience: '',
    language: 'English', duration_minutes: 5, aspect_ratio: '16:9', visual_style: '', outline: '',
  };
}

export function validateVideoBrief(value: unknown): VideoBrief {
  if (!object(value) || typeof value.id !== 'string' || !/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$/.test(value.id)) {
    throw new Error('Invalid video brief ID');
  }
  const { id, ...fields } = value;
  for (const key of [...Object.keys(VIDEO_BRIEF_LIMITS), 'duration_minutes']) {
    if (!Object.hasOwn(fields, key)) throw new Error(`Missing brief field: ${key}`);
  }
  return applyVideoBriefUpdate({ id } as VideoBrief, fields);
}

export function videoProjectDir(assetsRoot: string, id: string): string {
  if (!assetsRoot.startsWith('/')) throw new Error('An absolute assets root is required');
  if (!/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$/.test(id)) throw new Error('Invalid video brief ID');
  return `${assetsRoot.replace(/\/+$/, '')}/videos/${id}`;
}

function missing(error: unknown): boolean {
  return object(error) && object(error.response) && error.response.status === 404;
}

async function optionalJSON(path: string): Promise<unknown | undefined> {
  try {
    return JSON.parse(await fetchFileText(path));
  } catch (error) {
    if (missing(error)) return undefined;
    throw new Error(`Could not read ${path}: ${videoErrorMessage(error)}`);
  }
}

export function videoErrorMessage(error: unknown): string {
  if (object(error) && object(error.response)) {
    const data = error.response.data;
    if (typeof data === 'string' && data.trim()) return data.trim();
    if (object(data) && typeof data.message === 'string') return data.message;
  }
  return error instanceof Error ? error.message : 'Unexpected video project error';
}

export async function loadVideoProjects(assetsRoot: string, onError?: (message: string) => void): Promise<VideoProject[]> {
  if (!assetsRoot) return [];
  const root = videoProjectDir(assetsRoot, 'placeholder').replace(/\/placeholder$/, '');
  let entries;
  try {
    entries = (await browseFiles(root)).entries;
  } catch (error) {
    if (missing(error)) return [];
    throw error;
  }
  const projects: VideoProject[] = [];
  for (const entry of entries.filter((entry) => entry.is_dir)) {
    try {
      const dir = videoProjectDir(assetsRoot, entry.name);
      const raw = await optionalJSON(`${dir}/brief.json`);
      if (raw === undefined) continue;
      const brief = validateVideoBrief(raw);
      if (brief.id !== entry.name) throw new Error('Brief ID does not match its directory');
      const [submission, output] = await Promise.all([
        optionalJSON(`${dir}/submission.json`), optionalJSON(`${dir}/video.json`),
      ]);
      if (submission !== undefined && (!object(submission) || typeof submission.task_id !== 'string' || !submission.task_id.trim())) {
        throw new Error('Invalid submission.json task_id');
      }
      if (output !== undefined) {
        if (!object(output)) throw new Error('Invalid video.json output');
        for (const key of ['status', 'final_video', 'updated_at']) {
          if (key === 'final_video' && output[key] === null) continue;
          if (output[key] !== undefined && typeof output[key] !== 'string') throw new Error(`Invalid output ${key}`);
        }
        if (output.duration_s != null && (typeof output.duration_s !== 'number' || !Number.isFinite(output.duration_s) || output.duration_s < 0)) {
          throw new Error('Invalid output duration_s');
        }
      }
      projects.push({ brief, dir, task_id: (submission as { task_id: string } | undefined)?.task_id, output: output as VideoProject['output'] });
    } catch (error) {
      onError?.(`Could not load ${root}/${entry.name}: ${videoErrorMessage(error)}`);
    }
  }
  return projects.sort((a, b) => a.brief.title.localeCompare(b.brief.title) || a.brief.id.localeCompare(b.brief.id));
}

/** Writes only draft input. Submitted projects must never be edited. */
export async function saveVideoBrief(assetsRoot: string, value: VideoBrief): Promise<VideoProject> {
  const brief = validateVideoBrief(value);
  const dir = videoProjectDir(assetsRoot, brief.id);
  const [submission, output] = await Promise.all([
    optionalJSON(`${dir}/submission.json`), optionalJSON(`${dir}/video.json`),
  ]);
  if (submission !== undefined || output !== undefined) throw new Error('Submitted video briefs are read-only. Create a new draft instead.');
  await uploadFile(new File([JSON.stringify(brief, null, 2)], 'brief.json', { type: 'application/json' }), dir, 'brief.json');
  return { brief, dir };
}

/** UI-owned receipt; never read-modify-write the agent-owned video.json. */
export async function saveVideoSubmission(dir: string, taskId: string): Promise<void> {
  if (!taskId.trim()) throw new Error('A task ID is required');
  await uploadFile(new File([JSON.stringify({ task_id: taskId }, null, 2)], 'submission.json', { type: 'application/json' }), dir, 'submission.json');
}
