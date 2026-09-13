import type { ChatAttachment } from '@/lib/api/chat-sessions';

export const CHAT_ATTACHMENT_COUNT = 4;
export const CHAT_ATTACHMENT_TOTAL = 8 * 1024 * 1024;
const imageTypes = new Set(['image/png', 'image/jpeg', 'image/gif', 'image/webp']);

export function attachmentIsImage(file: ChatAttachment): boolean {
  return imageTypes.has(file.media_type);
}

export function attachmentBytes(file: ChatAttachment): number {
  return Math.floor(file.data.length * 3 / 4) - (file.data.endsWith('==') ? 2 : file.data.endsWith('=') ? 1 : 0);
}

export function attachmentSize(bytes: number): string {
  return bytes < 1024 * 1024 ? `${Math.max(1, Math.ceil(bytes / 1024))} KB` : `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

export function attachmentImageURL(file: ChatAttachment): string {
  return attachmentIsImage(file) ? `data:${file.media_type};base64,${file.data}` : '';
}

export async function readChatAttachment(file: File): Promise<ChatAttachment> {
  if (!file.size) throw new Error(`${file.name} is empty.`);
  if (file.size > 5 * 1024 * 1024) throw new Error(`${file.name} exceeds 5 MB.`);
  if (new TextEncoder().encode(file.name).length > 255 || /[\u0000-\u001f\u007f]/.test(file.name)) throw new Error('Use a shorter filename without control characters.');
  const data = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(',')[1]);
    reader.onerror = () => reject(new Error(`Could not read ${file.name}. Try adding it again.`));
    reader.readAsDataURL(file);
  });
  return { name: file.name, media_type: file.type || 'application/octet-stream', data };
}

export function downloadChatAttachment(file: ChatAttachment): void {
  const binary = atob(file.data);
  const bytes = Uint8Array.from(binary, c => c.charCodeAt(0));
  // Always download documents as bytes; never navigate to user-supplied HTML.
  const url = URL.createObjectURL(new Blob([bytes], { type: 'application/octet-stream' }));
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = file.name;
  anchor.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
