import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const t = await import(await moduleURL(new URL('../src/lib/helper/trace-view.ts', import.meta.url)));

const at = (ms) => new Date(Date.UTC(2026, 8, 1, 12, 0, 0) + ms).toISOString();

test('observation tree nests by parent, orders by start and aggregates subtrees', () => {
  const obs = [
    { id: 'tool', parent_observation_id: 'gen', observation_type: 'tool', name: 'search', started_at: at(2200), ended_at: at(2500), level: 'error' },
    { id: 'root', observation_type: 'agent', name: 'support', started_at: at(0), ended_at: at(4000) },
    { id: 'gen2', parent_observation_id: 'root', started_at: at(3000), ended_at: at(3900), input_tokens: 5, output_tokens: 5, cost_cents: 0.5 },
    { id: 'gen', parent_observation_id: 'root', started_at: at(100), ended_at: at(2100), input_tokens: 100, output_tokens: 20, cost_cents: 1 },
    { id: 'orphan', parent_observation_id: 'missing', created_at: at(5000), latency_ms: 200 },
  ];
  const roots = t.buildObservationTree(obs);
  assert.deepEqual(roots.map((r) => r.obs.id), ['root', 'orphan']);
  const root = roots[0];
  assert.deepEqual(root.children.map((c) => c.obs.id), ['gen', 'gen2']);
  assert.equal(root.children[0].children[0].depth, 2);
  assert.equal(root.tokens, 130);
  assert.equal(root.costCents, 1.5);
  assert.equal(root.errors, 1);
  // Legacy rows without started_at derive it from created_at - latency.
  assert.equal(roots[1].end - roots[1].start, 200);

  const rows = t.flattenTree(roots, new Set(['gen']));
  assert.deepEqual(rows.map((r) => r.obs.id), ['root', 'gen', 'gen2', 'orphan']);
  assert.deepEqual(t.ancestorIDs(roots, 'tool'), ['root', 'gen']);
});

test('cyclic parents never drop or loop', () => {
  const roots = t.buildObservationTree([
    { id: 'a', parent_observation_id: 'b', created_at: at(0) },
    { id: 'b', parent_observation_id: 'a', created_at: at(1) },
    { id: 'c', parent_observation_id: 'c', created_at: at(2) },
  ]);
  assert.equal(t.flattenTree(roots).length, 3);
});

test('waterfall geometry stays within the timeline and keeps a visible minimum', () => {
  const tl = { start: 0, end: 1000, duration: 1000 };
  assert.deepEqual(t.barGeometry(tl, 250, 500), { left: 25, width: 25 });
  assert.equal(t.barGeometry(tl, 500, 500).width, 0.4);
  const late = t.barGeometry(tl, 999, 1000);
  assert.ok(late.left + late.width <= 100.4);
  assert.deepEqual(t.barGeometry({ start: 0, end: 0, duration: 0 }, 0, 0), { left: 0, width: 100 });
  assert.deepEqual(t.timelineTicks(4820, 5), [0, 1000, 2000, 3000, 4000]);
});

test('request conversations: OpenAI, Anthropic canonical loop and Responses', () => {
  const openai = t.parseRequestConversation(JSON.stringify({
    model: 'gpt-5',
    messages: [
      { role: 'system', content: 'You are support.' },
      { role: 'user', content: 'Where is my order?' },
      { role: 'assistant', content: null, tool_calls: [{ id: 'c1', type: 'function', function: { name: 'search_orders', arguments: '{"user_id":"218"}' } }] },
      { role: 'tool', tool_call_id: 'c1', content: '{"status":"shipped"}' },
    ],
    tools: [{ type: 'function', function: { name: 'search_orders', description: 'Find orders' } }],
  }));
  assert.equal(openai.system, 'You are support.');
  assert.equal(openai.messages.length, 3);
  assert.equal(openai.messages[1].toolCalls[0].name, 'search_orders');
  assert.equal(openai.messages[2].toolCallID, 'c1');
  assert.deepEqual(openai.tools, [{ name: 'search_orders', description: 'Find orders' }]);

  const canonical = t.parseRequestConversation(JSON.stringify({
    model: 'claude',
    messages: [
      { role: 'system', content: 'sys' },
      { role: 'user', content: 'hi' },
      { role: 'assistant', content: [{ type: 'thinking', thinking: 'hmm' }, { type: 'text', text: 'calling' }, { type: 'tool_use', id: 't1', name: 'bash', input: { cmd: 'ls' } }] },
      { role: 'user', content: [{ type: 'tool_result', tool_use_id: 't1', content: 'a.txt' }, { type: 'image', source: {} }] },
    ],
  }));
  const assistant = canonical.messages[1];
  assert.equal(assistant.reasoning, 'hmm');
  assert.equal(assistant.text, 'calling');
  assert.equal(assistant.toolCalls[0].arguments.includes('"cmd"'), true);
  assert.deepEqual(canonical.messages[2].toolResults, [{ toolCallID: 't1', content: 'a.txt', isError: false }]);
  assert.deepEqual(canonical.messages[2].attachments, ['image']);

  const responses = t.parseRequestConversation(JSON.stringify({ instructions: 'be brief', input: 'question' }));
  assert.equal(responses.system, 'be brief');
  assert.equal(responses.messages[0].text, 'question');

  assert.equal(t.parseRequestConversation('not json'), null);
  assert.equal(t.parseRequestConversation(JSON.stringify({ prompt: 'x' })), null);
});

test('response messages: OpenAI, Anthropic, Responses and AT LLMResponse', () => {
  const openai = t.parseResponseMessage(JSON.stringify({ choices: [{ message: { role: 'assistant', content: 'Your package', tool_calls: [{ id: 'x', function: { name: 'f', arguments: '{}' } }] } }] }));
  assert.equal(openai.text, 'Your package');
  assert.equal(openai.toolCalls[0].name, 'f');
  const anthropic = t.parseResponseMessage(JSON.stringify({ type: 'message', role: 'assistant', content: [{ type: 'text', text: 'hello' }] }));
  assert.equal(anthropic.text, 'hello');
  const responses = t.parseResponseMessage(JSON.stringify({ output: [{ type: 'message', content: [{ type: 'output_text', text: 'done' }] }] }));
  assert.equal(responses.text, 'done');
  const loop = t.parseResponseMessage(JSON.stringify({ Content: 'loop answer', ToolCalls: [{ ID: 'a', Name: 'bash', Arguments: { cmd: 'ls' } }], Refusal: '' }));
  assert.equal(loop.text, 'loop answer');
  assert.equal(loop.toolCalls[0].name, 'bash');
  assert.equal(t.parseResponseMessage('{}'), null);
});

test('filters round-trip through the URL and render removable chips', () => {
  const query = new URLSearchParams('model=gpt-5,claude&min_latency_ms=2000&status=error&bookmarked=true&order=asc&task_ids=a,b&bogus=1');
  const f = t.filtersFromQuery(query);
  assert.deepEqual(f.model, ['gpt-5', 'claude']);
  assert.deepEqual(f.task_id, ['a', 'b']);
  assert.equal(f.order, 'asc');
  const chips = t.filterChips(f);
  assert.deepEqual(chips.map((c) => c.label), ['Model: gpt-5', 'Model: claude', 'Task: a', 'Task: b', 'Has errors', 'Latency ≥ 2.00s', 'Bookmarked']);
  const without = t.removeChip(f, chips[0]);
  assert.deepEqual(without.model, ['claude']);
  assert.deepEqual(f.model, ['gpt-5', 'claude'], 'removeChip must not mutate its input');
  const out = t.filtersToQuery(without);
  assert.equal(out.model, 'claude');
  assert.equal(out.task_id, 'a,b');
  assert.equal(out.task_ids, null);
  assert.equal(out.order, 'asc');
  const params = t.filtersToParams(t.removeChip(without, chips.find((c) => c.key === 'latency')));
  assert.equal(params.min_latency_ms, undefined);
  assert.deepEqual(params.model, ['claude']);
  assert.equal(params.bookmarked, 'true');
  assert.equal(t.filtersFromQuery(new URLSearchParams('status=maybe')).status, '');
});

test('formatting', () => {
  assert.equal(t.formatDurationMs(420), '420ms');
  assert.equal(t.formatDurationMs(4820), '4.82s');
  assert.equal(t.formatDurationMs(872000), '14m 32s');
  assert.equal(t.formatCost(2.14), '$0.0214');
  assert.equal(t.formatCost(0.05), '$0.00050');
  assert.equal(t.formatTokens(6421), '6,421');
  assert.equal(t.formatTokens(12802), '12.8k');
  assert.equal(t.formatScore({ data_type: 'boolean', average: 1 }), 'true');
  assert.equal(t.formatScore({ data_type: 'numeric', average: 0.923 }), '0.92');
});
