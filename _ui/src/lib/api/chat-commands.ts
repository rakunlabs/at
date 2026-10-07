import axios from 'axios';

const api = axios.create({ baseURL: 'api/v1' });

/** A slash command: a named prompt template run as `/name arguments`. */
export interface ChatCommand {
  /** Server-assigned. Send `''` to create a new entry. */
  id: string;
  name: string;
  description?: string;
  template: string;
  /** Optional `provider/model` used for this one message. */
  model?: string;
  created_at?: string;
  updated_at?: string;
  scope: 'personal' | 'workspace';
  can_edit: boolean;
  owner_user_id?: string;
}

export type ChatCommandInput = Pick<ChatCommand, 'name' | 'description' | 'template' | 'model'>;

function body(command: ChatCommandInput & { id?: string }) {
  return { id: command.id, name: command.name, description: command.description ?? '', template: command.template, model: command.model ?? '' };
}

export async function listChatCommands(): Promise<ChatCommand[]> {
  const res = await api.get<{ commands: ChatCommand[] }>('/chats/commands');
  return (res.data?.commands ?? []).map(c => ({ ...c, scope: 'personal', can_edit: true }));
}

/** Replaces the whole personal list; identity is resolved server-side. */
export async function saveChatCommands(commands: Array<ChatCommandInput & { id?: string }>): Promise<ChatCommand[]> {
  const res = await api.put<{ commands: ChatCommand[] }>('/chats/commands', { commands: commands.map(body) });
  return (res.data?.commands ?? []).map(c => ({ ...c, scope: 'personal', can_edit: true }));
}

export async function listWorkspaceChatCommands(): Promise<ChatCommand[]> {
  const res = await api.get<{ commands: ChatCommand[] }>('/chats/workspace-commands');
  return (res.data?.commands ?? []).map(c => ({ ...c, scope: 'workspace', can_edit: !!c.can_edit }));
}

export async function createWorkspaceChatCommand(command: ChatCommandInput): Promise<ChatCommand> {
  const res = await api.post<ChatCommand>('/chats/workspace-commands', body(command));
  return { ...res.data, scope: 'workspace', can_edit: true };
}

export async function updateWorkspaceChatCommand(id: string, command: ChatCommandInput): Promise<ChatCommand> {
  const res = await api.put<ChatCommand>(`/chats/workspace-commands/${encodeURIComponent(id)}`, body(command));
  return { ...res.data, scope: 'workspace', can_edit: true };
}

export async function deleteWorkspaceChatCommand(id: string): Promise<void> {
  await api.delete(`/chats/workspace-commands/${encodeURIComponent(id)}`);
}
