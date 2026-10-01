export interface ChatTurn {
  controller: AbortController;
  traceId: string;
  generation: number;
}

/** Own the whole turn, including persistence, tools and follow-up generations. */
export function createChatTurnLifecycle() {
  let generation = 0;
  let active: ChatTurn | null = null;
  const current = (turn: ChatTurn) => active === turn && turn.generation === generation;
  return {
    begin(): ChatTurn | null {
      if (active) return null;
      active = {
        controller: new AbortController(), generation,
        traceId: `chats-turn-${Array.from(crypto.getRandomValues(new Uint8Array(12)), b => b.toString(16).padStart(2, '0')).join('')}`,
      };
      return active;
    },
    current,
    assert(turn: ChatTurn) {
      turn.controller.signal.throwIfAborted();
      if (!current(turn)) throw new DOMException('Chat changed', 'AbortError');
    },
    finish(turn: ChatTurn) { if (current(turn)) active = null; },
    invalidate() {
      generation++;
      active?.controller.abort();
      active = null;
    },
    generation: () => generation,
  };
}

/** Iterative rather than recursive: one owner and one cancellation scope. */
export async function runChatIterations(max: number, step: (iteration: number) => Promise<boolean>, assertCurrent: () => void): Promise<boolean> {
  for (let iteration = 0; iteration < max; iteration++) {
    assertCurrent();
    const again = await step(iteration);
    assertCurrent();
    if (!again) return true;
  }
  return false;
}

export function parseChatToolArguments(value: string): Record<string, unknown> {
  let args: unknown;
  try { args = JSON.parse(value); } catch {
    throw new Error('Tool arguments must be valid JSON. No tool was executed.');
  }
  if (!args || typeof args !== 'object' || Array.isArray(args)) {
    throw new Error('Tool arguments must be a JSON object. No tool was executed.');
  }
  return args as Record<string, unknown>;
}
