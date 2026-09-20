import type { ChatMessage } from './chat';

/** Match within an assistant turn, not by name or a globally unique ID assumption. */
export function toolResultsByMessage(messages: ChatMessage[]): Map<number, Map<string, ChatMessage>> {
  const results = new Map<number, Map<string, ChatMessage>>();
  let owner = -1;
  let ids = new Set<string>();
  messages.forEach((message, index) => {
    if (message.role === 'assistant') {
      owner = index;
      ids = new Set(message.tool_calls?.map(call => call.id) || []);
      if (ids.size) results.set(owner, new Map());
    } else if (message.role === 'tool') {
      if (message.tool_call_id && ids.has(message.tool_call_id)) results.get(owner)?.set(message.tool_call_id, message);
    } else {
      owner = -1;
      ids = new Set();
    }
  });
  return results;
}

export function formatToolPayload(value: string): string {
  try { return JSON.stringify(JSON.parse(value), null, 2); }
  catch { return value; }
}

export function toolResultFailed(value: string): boolean {
  if (/^Error:/i.test(value.trimStart())) return true;
  try {
    const parsed = JSON.parse(value);
    return !!parsed && typeof parsed === 'object' && (parsed.isError === true || !!parsed.error);
  } catch { return false; }
}
