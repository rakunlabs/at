import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/chat-tool-selections.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { normalizeChatToolSelections, isChatTodoTool } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('legacy server todos migrate to Chat tools without duplication or mutation', () => {
  const builtin = ['todo_write', 'current_time', 'todo_read'];
  const frontend = ['todo_read', 'question'];
  const normalized = normalizeChatToolSelections(builtin, frontend);
  assert.deepEqual(normalized, { builtin_tools: ['current_time'], frontend_tools: ['todo_read', 'question', 'todo_write'] });
  assert.deepEqual(builtin, ['todo_write', 'current_time', 'todo_read']);
  assert.deepEqual(frontend, ['todo_read', 'question']);
  assert.deepEqual(normalizeChatToolSelections(normalized.builtin_tools, normalized.frontend_tools), normalized);
});

test('explicit no-tools selections remain empty', () => {
  assert.deepEqual(normalizeChatToolSelections([], []), { builtin_tools: [], frontend_tools: [] });
  assert.equal(isChatTodoTool('todo_read'), true);
  assert.equal(isChatTodoTool('todo_write'), true);
  assert.equal(isChatTodoTool('current_time'), false);
});

test('Chats hides server todo implementations from its tool catalog', async () => {
  const chat = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');
  assert.match(chat, /tools=\{builtinTools\.filter\(tool => !isChatTodoTool\(tool\.name\)\)\}/);
  assert.match(chat, /if \(isChatTodoTool\(toolName\)\) continue;/);
});

test('built-in calls carry the active turn session as a per-request header', async () => {
  const chat = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');
  const api = await readFile(new URL('../src/lib/api/mcp.ts', import.meta.url), 'utf8');
  assert.match(chat, /callBuiltinTool\(tc\.function\.name, args, '', turn\.traceId, turn\.sessionId, turn\.controller\.signal\)/);
  assert.match(api, /headers: \{ 'X-Session-ID': sessionId \}/);
});
