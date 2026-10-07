import type { ChatMessage } from './chat';

export interface TodoItem {
  content: string;
  status: 'pending' | 'in_progress' | 'completed' | 'cancelled';
  priority: 'high' | 'medium' | 'low';
}

const STATUSES = new Set(['pending', 'in_progress', 'completed', 'cancelled']);
const PRIORITIES = new Set(['high', 'medium', 'low']);

export function normalizeTodos(items: unknown[]): TodoItem[] {
  return items.map((t: any) => ({
    content: String(t?.content || ''),
    status: STATUSES.has(t?.status) ? t.status : 'pending',
    priority: PRIORITIES.has(t?.priority) ? t.priority : 'medium',
  }));
}

/**
 * The todo list is browser state written by the `todo_write` tool, and every
 * call is stored in the transcript. Rebuilding it from the latest successful
 * call makes the panel survive navigation and reloads without its own storage.
 * Returns null when the loaded messages contain no such call.
 */
export function latestTodos(messages: ChatMessage[]): TodoItem[] | null {
  const failed = new Set<string>();
  for (const m of messages) {
    if (m.role === 'tool' && m.tool_call_id && typeof m.content === 'string' && m.content.startsWith('Error')) failed.add(m.tool_call_id);
  }
  for (let i = messages.length - 1; i >= 0; i--) {
    const calls = messages[i].tool_calls;
    if (messages[i].role !== 'assistant' || !calls?.length) continue;
    for (let j = calls.length - 1; j >= 0; j--) {
      const call = calls[j];
      if (call.function?.name !== 'todo_write' || failed.has(call.id)) continue;
      try {
        const args = JSON.parse(call.function.arguments || '{}');
        if (Array.isArray(args?.todos)) return normalizeTodos(args.todos);
      } catch {
        // A malformed call never changed the list; look further back.
      }
    }
  }
  return null;
}
