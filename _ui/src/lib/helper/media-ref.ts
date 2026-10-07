// Stored media referenced from Markdown as `![alt](media:<id>)`.
//
// generate_image (and other media tools) in Chats return a ready snippet in
// this form, because a model cannot know the workspace-scoped URL the browser
// needs. The reference is resolved at render time, so the stored transcript
// stays independent of the deployment path and the workspace selector.

const MEDIA_REF = /(!?)\[([^\]\n]*)\]\(\s*<?media:([0-9A-Za-z_-]{1,128})>?(?:\s+"[^"\n]*")?\s*\)/g;

/** IDs referenced as `[..](media:<id>)` or `![..](media:<id>)` in the text. */
export function mediaRefIDs(text: string): Set<string> {
  const ids = new Set<string>();
  if (!text || !text.includes('media:')) return ids;
  for (const match of text.matchAll(MEDIA_REF)) ids.add(match[3]);
  return ids;
}

/**
 * Rewrites every media reference with `urlFor(id)`. When `urlFor` returns an
 * empty string the media cannot be shown here (for example a share without
 * attachments), so only the alt/link text is kept instead of a broken image.
 */
export function resolveMediaRefs(text: string, urlFor: (id: string) => string): string {
  if (!text || !text.includes('media:')) return text;
  return text.replace(MEDIA_REF, (_, bang: string, label: string, id: string) => {
    const url = urlFor(id);
    if (!url) return label ? `*${label}*` : '';
    return `${bang}[${label}](<${url}>)`;
  });
}

/** Text of every text part (or the string content) of a message. */
export function messageText(content: unknown): string {
  if (typeof content === 'string') return content;
  if (!Array.isArray(content)) return '';
  return content.filter(part => part && typeof part === 'object' && (part as any).type === 'text').map(part => String((part as any).text ?? '')).join('\n');
}
