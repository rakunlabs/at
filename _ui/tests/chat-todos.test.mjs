import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const { latestTodos, normalizeTodos } = await import(await moduleURL(new URL('../src/lib/helper/chat-todos.ts', import.meta.url)));

const call = (id, args) => ({ id, type: 'function', function: { name: 'todo_write', arguments: typeof args === 'string' ? args : JSON.stringify(args) } });

test('the latest successful todo_write call restores the list', () => {
  const messages = [
    { role: 'user', content: 'plan' },
    { role: 'assistant', content: '', tool_calls: [call('a', { todos: [{ content: 'one', status: 'pending' }] })] },
    { role: 'tool', tool_call_id: 'a', content: '{"success":true}' },
    { role: 'assistant', content: '', tool_calls: [call('b', { todos: [{ content: 'one', status: 'completed', priority: 'high' }] })] },
    { role: 'tool', tool_call_id: 'b', content: '{"success":true}' },
    { role: 'assistant', content: 'done' },
  ];
  assert.deepEqual(latestTodos(messages), [{ content: 'one', status: 'completed', priority: 'high' }]);
});

test('failed and malformed calls are skipped; no call returns null', () => {
  const messages = [
    { role: 'assistant', content: '', tool_calls: [call('a', { todos: [{ content: 'kept' }] })] },
    { role: 'tool', tool_call_id: 'a', content: '{"success":true}' },
    { role: 'assistant', content: '', tool_calls: [call('b', { todos: 'nope' }), call('c', '{broken')] },
    { role: 'tool', tool_call_id: 'b', content: 'Error: todos must be an array' },
  ];
  assert.deepEqual(latestTodos(messages), [{ content: 'kept', status: 'pending', priority: 'medium' }]);
  assert.equal(latestTodos([{ role: 'user', content: 'hi' }]), null);
  assert.deepEqual(latestTodos([{ role: 'assistant', content: '', tool_calls: [call('d', { todos: [] })] }]), []);
});

test('unknown status and priority fall back to defaults', () => {
  assert.deepEqual(normalizeTodos([{ content: 'x', status: 'weird', priority: 'urgent' }]), [{ content: 'x', status: 'pending', priority: 'medium' }]);
});
