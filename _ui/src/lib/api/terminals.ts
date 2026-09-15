import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1/terminals' });

export interface TerminalSession {
  id: string;
  target_id: string;
  target_name: string;
  username: string;
  title: string;
  position: number;
}
export interface TerminalTarget { id: string; name: string; available: boolean; reason?: string }
export interface LinuxUser { name: string; uid: string; home: string; shell: string }
export type TerminalAppearance = 'dark' | 'light' | 'system';
export interface TerminalPreferences {
  active_id: string;
  default_users: Record<string, string>;
  // Terminal palette, independent of the page theme. Empty means dark.
  appearance?: TerminalAppearance;
  // Font family name, resolved on this device. Empty uses the monospace stack.
  font_family?: string;
  // Font size in pixels, 10–28. Empty means 14.
  font_size?: number;
}
export interface TerminalList {
  sessions: TerminalSession[];
  targets: TerminalTarget[];
  preferences: TerminalPreferences;
}
export async function listTerminals(): Promise<TerminalList> { return (await api.get('')).data; }
export async function terminalUsers(target: string): Promise<LinuxUser[]> { return (await api.get(`targets/${encodeURIComponent(target)}/users`)).data; }
export async function createTerminal(target_id: string, username: string, title: string): Promise<TerminalSession> { return (await api.post('', { target_id, username, title })).data; }
export async function updateTerminal(item: TerminalSession): Promise<void> { await api.put(encodeURIComponent(item.id), { title: item.title, position: item.position }); }
export async function deleteTerminal(id: string): Promise<void> { await api.delete(encodeURIComponent(id)); }
export async function startTerminal(id: string): Promise<void> { await api.post(`${encodeURIComponent(id)}/start`, {}); }
export async function saveTerminalPreferences(value: TerminalPreferences): Promise<void> { await api.put('preferences', value); }

export function terminalSocketURL(id: string): string {
  const url = new URL(`api/v1/terminals/${encodeURIComponent(id)}/ws`, document.baseURI);
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
  return url.href;
}
