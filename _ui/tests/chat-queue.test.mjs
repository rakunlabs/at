import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const { queuedMessage, takeQueued, userMessageContent, queuedPreview } = await import(await moduleURL(new URL('../src/lib/helper/chat-queue.ts', import.meta.url)));

test('queued entries keep order and unique ids', () => {
  const a = queuedMessage('one', []);
  const b = queuedMessage('two', []);
  assert.notEqual(a.id, b.id);
  const [rest, taken] = takeQueued([a, b], a.id);
  assert.equal(taken, a);
  assert.deepEqual(rest, [b]);
  const [same, none] = takeQueued(rest, 'missing');
  assert.equal(none, null);
  assert.equal(same, rest);
});

test('content puts attachments before text and stays a string without them', () => {
  assert.equal(userMessageContent('hi', [], () => ({})), 'hi');
  const parts = userMessageContent('look', [{ name: 'a.png' }], a => ({ type: 'image_url', name: a.name }));
  assert.deepEqual(parts, [{ type: 'image_url', name: 'a.png' }, { type: 'text', text: 'look' }]);
  assert.deepEqual(userMessageContent('', [{ name: 'a' }], a => a), [{ name: 'a' }]);
});

test('preview collapses whitespace, clips and counts files', () => {
  const item = queuedMessage('a\n\n  b', [{ name: 'x' }, { name: 'y' }]);
  assert.equal(queuedPreview(item), 'a b [2 files]');
  assert.equal(queuedPreview(queuedMessage('abcdef', []), 4), 'abc…');
});

const { unansweredToolCalls, isEmptyAssistant } = await import(await moduleURL(new URL('../src/lib/helper/chat-queue.ts', import.meta.url)));

test('unanswered tool calls are found only for the latest assistant step', () => {
  const calls = [{ id: 'a' }, { id: 'b' }];
  assert.deepEqual(unansweredToolCalls([{ role: 'user' }, { role: 'assistant', tool_calls: calls }, { role: 'tool', tool_call_id: 'a' }]), ['b']);
  assert.deepEqual(unansweredToolCalls([{ role: 'assistant', tool_calls: calls }, { role: 'user' }]), []);
  assert.deepEqual(unansweredToolCalls([{ role: 'assistant', content: 'hi' }]), []);
});

test('only a content-less assistant without calls is an empty placeholder', () => {
  assert.equal(isEmptyAssistant({ role: 'assistant', content: '' }), true);
  assert.equal(isEmptyAssistant({ role: 'assistant', content: [] }), true);
  assert.equal(isEmptyAssistant({ role: 'assistant', content: 'x' }), false);
  assert.equal(isEmptyAssistant({ role: 'assistant', content: '', tool_calls: [{ id: 'a' }] }), false);
  assert.equal(isEmptyAssistant({ role: 'user', content: '' }), false);
});
