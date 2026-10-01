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
