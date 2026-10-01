import type { PlaygroundDefaults } from '../api/playground';

/** Chats owns these tools in the browser so they update its visible todo panel. */
export function isChatTodoTool(name: string): boolean {
  return name === 'todo_write' || name === 'todo_read';
}

/** Restore old server todo selections without advertising two implementations. */
export function normalizeChatToolSelections(builtin: string[], frontend: string[]) {
  return {
    builtin_tools: builtin.filter(name => !isChatTodoTool(name)),
    frontend_tools: [...new Set([...frontend, ...builtin.filter(isChatTodoTool)])],
  };
}

export interface WorkbenchSetup extends Required<PlaygroundDefaults> {}

function names(value: unknown): string[] {
  return Array.isArray(value)
    ? [...new Set(value.filter((name): name is string => typeof name === 'string'))]
    : [];
}

/** Saved empty selections stay empty; only absent browser tools use defaults. */
export function normalizeWorkbenchSetup(input: PlaygroundDefaults | Record<string, unknown>, frontendDefaults: string[] = []): WorkbenchSetup {
  const tools = normalizeChatToolSelections(names(input.builtin_tools), names(input.frontend_tools ?? frontendDefaults));
  return {
    model: typeof input.model === 'string' ? input.model : '',
    reasoning_effort: typeof input.reasoning_effort === 'string' ? input.reasoning_effort : '',
    system_prompt: typeof input.system_prompt === 'string' ? input.system_prompt : '',
    mcp_sets: names(input.mcp_sets),
    skills: names(input.skills),
    ...tools,
  };
}

export function initialWorkbenchSetup(frontendDefaults: string[]): WorkbenchSetup {
  return normalizeWorkbenchSetup({ builtin_tools: ['whoami'] }, frontendDefaults);
}

/** New chats take the whole saved setup, never a mix with the previous chat. */
export function newWorkbenchSetup(defaults: WorkbenchSetup, models: string[]): WorkbenchSetup {
  return normalizeWorkbenchSetup({
    ...defaults,
    model: models.includes(defaults.model) ? defaults.model : (models[0] ?? ''),
  });
}

export function workbenchSetupsEqual(left: WorkbenchSetup, right: WorkbenchSetup): boolean {
  const sameSelection = (a: string[], b: string[]) => {
    const sorted = [...b].sort();
    return a.length === b.length && [...a].sort().every((name, index) => name === sorted[index]);
  };
  return left.model === right.model
    && left.reasoning_effort === right.reasoning_effort
    && left.system_prompt === right.system_prompt
    && sameSelection(left.mcp_sets, right.mcp_sets)
    && sameSelection(left.skills, right.skills)
    && sameSelection(left.builtin_tools, right.builtin_tools)
    && sameSelection(left.frontend_tools, right.frontend_tools);
}
