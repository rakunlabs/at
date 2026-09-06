import { browseFiles, fetchFileText, uploadFile } from './files';
import { validateVideoBrief, videoErrorMessage, type VideoBrief } from './studio-videos';

export interface VideoTemplate {
  id: string;
  name: string;
  brief: VideoBrief;
  created_at: string;
  updated_at: string;
}

const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function templateRoot(assetsRoot: string): string {
  if (!assetsRoot.startsWith('/')) throw new Error('An absolute assets root is required');
  return `${assetsRoot.replace(/\/+$/, '')}/video-templates`;
}

function validateTemplate(value: unknown): VideoTemplate {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Invalid video template');
  const template = value as VideoTemplate;
  if (typeof template.id !== 'string' || !uuid.test(template.id)) throw new Error('Invalid template UUID');
  if (typeof template.name !== 'string' || !template.name.trim()) throw new Error('A template name is required');
  for (const key of ['created_at', 'updated_at'] as const) {
    if (typeof template[key] !== 'string' || !Number.isFinite(Date.parse(template[key]))) throw new Error(`Invalid template ${key}`);
  }
  return {
    id: template.id, name: template.name.trim(), brief: validateVideoBrief(template.brief),
    created_at: template.created_at, updated_at: template.updated_at,
  };
}

export function createVideoTemplate(brief: VideoBrief, name: string): VideoTemplate {
  const now = new Date().toISOString();
  return validateTemplate({ id: crypto.randomUUID(), name, brief, created_at: now, updated_at: now });
}

export async function loadVideoTemplates(assetsRoot: string, onError?: (message: string) => void): Promise<VideoTemplate[]> {
  if (!assetsRoot) return [];
  const root = templateRoot(assetsRoot);
  let entries;
  try {
    entries = (await browseFiles(root)).entries;
  } catch (error) {
    if ((error as { response?: { status?: number } })?.response?.status === 404) return [];
    throw error;
  }
  const templates: VideoTemplate[] = [];
  for (const entry of entries.filter((entry) => !entry.is_dir && entry.name.endsWith('.json'))) {
    try {
      const id = entry.name.slice(0, -5);
      if (!uuid.test(id)) throw new Error('Invalid template filename UUID');
      const template = validateTemplate(JSON.parse(await fetchFileText(`${root}/${id}.json`)));
      if (template.id !== id) throw new Error('Template ID does not match its filename');
      templates.push(template);
    } catch (error) {
      onError?.(`Could not load template ${entry.name}: ${videoErrorMessage(error)}`);
    }
  }
  return templates.sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id));
}

/** Save an independent snapshot, never a project brief or production receipt. */
export async function saveVideoTemplate(assetsRoot: string, template: VideoTemplate): Promise<VideoTemplate> {
  const root = templateRoot(assetsRoot);
  const saved = validateTemplate({ ...template, updated_at: new Date().toISOString() });
  const name = `${saved.id}.json`;
  await uploadFile(new File([JSON.stringify(saved, null, 2)], name, { type: 'application/json' }), root, name);
  return saved;
}
