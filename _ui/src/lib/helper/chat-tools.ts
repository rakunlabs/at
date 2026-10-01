import type { ToolCall } from './chat';
import { parseChatToolArguments } from './chat-turn';

/** Route only validated arguments; missing or invalid calls never reach a tool. */
export async function dispatchChatTool<Source extends { type: string }>(
  call: ToolCall,
  sources: Record<string, Source>,
  handlers: Record<string, (source: Source, args: Record<string, unknown>) => Promise<string>>,
): Promise<string> {
  try {
    const args = parseChatToolArguments(call.function.arguments);
    const source = sources[call.function.name];
    if (!source) return `Error: no handler found for tool "${call.function.name}"`;
    const handler = handlers[source.type];
    if (!handler) return 'Error: unknown tool source type';
    return await handler(source, args);
  } catch (e: any) {
    if (e?.name === 'AbortError' || e?.code === 'ERR_CANCELED') throw e;
    return `Error: ${e?.response?.data?.message || e?.response?.data?.error?.message || e?.message || 'tool execution failed'}`;
  }
}
