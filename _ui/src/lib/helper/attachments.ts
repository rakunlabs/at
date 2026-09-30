// Chat attachments: which model input modality a file needs and the
// OpenAI-shape content part that carries it. The gateway adapters translate
// these four part shapes per provider (image_url, file, input_audio,
// video_url). Mirrors internal/server/gateway-input-modalities.go.

export type InputModality = 'text' | 'image' | 'pdf' | 'audio' | 'video';

export const INPUT_MODALITIES: InputModality[] = ['text', 'image', 'pdf', 'audio', 'video'];

/** Text files of any kind are read by every model; no capability needed. */
const TEXT_TYPES = new Set([
  'application/json', 'application/xml', 'application/sql', 'application/x-sh', 'application/javascript',
  'application/x-yaml', 'application/yaml', 'application/toml', 'application/x-httpd-php',
]);

const TEXT_EXTENSIONS = new Set([
  'txt', 'md', 'markdown', 'csv', 'tsv', 'json', 'jsonl', 'yaml', 'yml', 'toml', 'xml', 'html', 'htm', 'css',
  'sql', 'log', 'ini', 'conf', 'env', 'sh', 'bash', 'zsh', 'ps1', 'bat', 'js', 'mjs', 'cjs', 'ts', 'tsx', 'jsx',
  'svelte', 'vue', 'go', 'py', 'rb', 'rs', 'java', 'kt', 'kts', 'scala', 'c', 'h', 'cc', 'cpp', 'hpp', 'cs',
  'swift', 'm', 'php', 'pl', 'lua', 'r', 'dart', 'ex', 'exs', 'erl', 'hs', 'clj', 'tf', 'proto', 'graphql',
  'gql', 'dockerfile', 'makefile', 'gradle', 'properties', 'rst', 'tex', 'srt', 'vtt',
]);

function extension(name: string): string {
  const lower = name.toLowerCase();
  const dot = lower.lastIndexOf('.');
  return dot >= 0 ? lower.slice(dot + 1) : lower;
}

/**
 * How a file reaches the model: `text` means its contents are sent as text
 * (any model), a modality means the model must declare it, and `null` means
 * no provider parses it (docx, zip…) — it is refused rather than sent.
 */
export function attachmentModality(name: string, mime: string): InputModality | null {
  const type = (mime || '').toLowerCase().split(';')[0].trim();
  if (type === 'application/pdf' || extension(name) === 'pdf') return 'pdf';
  if (type.startsWith('image/') && type !== 'image/svg+xml') return 'image';
  if (type.startsWith('audio/')) return 'audio';
  if (type.startsWith('video/')) return 'video';
  if (type.startsWith('text/') || TEXT_TYPES.has(type) || TEXT_EXTENSIONS.has(extension(name))) return 'text';
  return null;
}

export interface PendingAttachment {
  name: string;
  mime: string;
  modality: InputModality;
  /** data: URL for binary media; the decoded contents for text files. */
  dataUrl: string;
  text?: string;
  size: number;
}

/** Content part that carries one attachment to /chats/completions. */
export function attachmentPart(a: PendingAttachment): { type: 'text' | 'image_url' | 'file' | 'input_audio' | 'video_url'; [key: string]: unknown } {
  switch (a.modality) {
    case 'image':
      return { type: 'image_url', image_url: { url: a.dataUrl } };
    case 'pdf':
      return { type: 'file', file: { filename: a.name, file_data: a.dataUrl } };
    case 'audio': {
      const comma = a.dataUrl.indexOf(',');
      const subtype = (a.mime.split('/')[1] || 'wav').replace(/^x-/, '').replace('mpeg', 'mp3');
      return { type: 'input_audio', input_audio: { data: a.dataUrl.slice(comma + 1), format: subtype } };
    }
    case 'video':
      return { type: 'video_url', video_url: { url: a.dataUrl } };
    default:
      return { type: 'text', text: `<file name="${a.name.replace(/"/g, "'")}">\n${a.text ?? ''}\n</file>` };
  }
}

/** Which non-text modalities a content array needs. */
export function contentModalities(content: unknown): InputModality[] {
  if (!Array.isArray(content)) return [];
  const found = new Set<InputModality>();
  for (const part of content as Record<string, any>[]) {
    switch (part?.type) {
      case 'image_url':
        found.add('image');
        break;
      case 'input_audio':
        found.add('audio');
        break;
      case 'video_url':
        found.add('video');
        break;
      case 'file': {
        const data = part.file?.file_data;
        const mime = typeof data === 'string' ? /^data:([^;,]+)/.exec(data)?.[1] ?? '' : '';
        const modality = attachmentModality(part.file?.filename ?? '', mime);
        if (modality && modality !== 'text') found.add(modality);
        break;
      }
    }
  }
  return INPUT_MODALITIES.filter(m => found.has(m));
}

/**
 * Human text for why a file cannot be attached to this model, or '' when it
 * can. `accepted` undefined means the model's inputs are unknown: everything
 * is allowed and the provider decides.
 */
export function attachmentRefusal(a: Pick<PendingAttachment, 'name' | 'modality'> | { name: string; modality: InputModality | null }, accepted: InputModality[] | undefined): string {
  if (a.modality === null) {
    return `"${a.name}" cannot be sent to a model: no provider reads this file type. Convert it to PDF or text.`;
  }
  if (a.modality === 'text' || !accepted || accepted.includes(a.modality)) return '';
  return `The selected model does not accept ${a.modality} input ("${a.name}"). It accepts: ${accepted.join(', ')}.`;
}

/** The `accept` attribute for a file picker, from the model's inputs. */
export function acceptAttribute(accepted: InputModality[] | undefined): string {
  const inputs = accepted ?? INPUT_MODALITIES;
  const parts = ['text/*', ...[...TEXT_EXTENSIONS].map(e => `.${e}`)];
  if (inputs.includes('image')) parts.push('image/*');
  if (inputs.includes('pdf')) parts.push('application/pdf', '.pdf');
  if (inputs.includes('audio')) parts.push('audio/*');
  if (inputs.includes('video')) parts.push('video/*');
  return parts.join(',');
}
