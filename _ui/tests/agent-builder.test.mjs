import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/agent-builder.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { applyAgentDraftPatch } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
const catalog = {
  providers: [
    { key: 'openai', type: 'openai', models: ['model-a'], default_model: 'model-a' },
    { key: 'claude', type: 'anthropic', models: ['model-b'], default_model: 'model-b' },
  ],
  skills: [{ id: 'skill-id', name: 'research' }],
  mcp_sets: [{ id: 'mcp-id', name: 'browser' }],
  workflows: [{ id: 'workflow-id', name: 'report' }],
  builtin_tools: [{ id: 'file_read', name: 'file_read' }],
};
const draft = () => ({ name: 'Reviewer', description: 'Manual description', group: 'Engineering', provider: 'openai', model: 'model-a', reasoning_effort: 'xhigh', system_prompt: 'Original prompt', skills: ['old-skill'], mcp_sets: [], workflows: [], builtin_tools: [], max_iterations: 10, tool_timeout: 60 });

test('a prompt-only revision preserves manual form edits and absent fields', () => {
  const current = draft();
  const changed = applyAgentDraftPatch(current, { system_prompt: 'New prompt' }, catalog);
  assert.deepEqual(changed, { ...current, system_prompt: 'New prompt' });
  assert.equal(current.system_prompt, 'Original prompt');
  assert.notEqual(changed.skills, current.skills);
});

test('selection identifiers match the actual form: skill, MCP and workflow names', () => {
  const changed = applyAgentDraftPatch(draft(), { skills: ['research', 'research'], mcp_sets: ['browser'], workflows: ['report'], builtin_tools: ['file_read'] }, catalog);
  assert.deepEqual(changed.skills, ['research']);
  assert.deepEqual(changed.mcp_sets, ['browser']);
  assert.deepEqual(changed.workflows, ['report']);
  assert.throws(() => applyAgentDraftPatch(draft(), { mcp_sets: ['mcp-id'] }, catalog), /Unknown mcp_sets/);
  assert.throws(() => applyAgentDraftPatch(draft(), { skills: ['skill-id'] }, catalog), /Unknown skills/);
});

test('unknown selections reject the entire patch without partial edits', () => {
  const current = draft();
  const before = structuredClone(current);
  assert.throws(() => applyAgentDraftPatch(current, { name: 'Changed', skills: ['invented'] }, catalog), /Unknown skills/);
  assert.deepEqual(current, before);
  assert.deepEqual(applyAgentDraftPatch(current, { skills: [] }, catalog).skills, []);
  assert.deepEqual(applyAgentDraftPatch(current, { skills: ['old-skill'] }, catalog).skills, ['old-skill']);
});

test('changing provider resets stale model and incompatible reasoning together', () => {
  const changed = applyAgentDraftPatch(draft(), { provider: 'claude' }, catalog);
  assert.equal(changed.model, 'model-b');
  assert.equal(changed.reasoning_effort, '');
  assert.throws(() => applyAgentDraftPatch(draft(), { provider: 'claude', model: 'model-a' }, catalog), /Choose a model/);
  assert.throws(() => applyAgentDraftPatch(draft(), { provider: 'claude', reasoning_effort: 'xhigh' }, catalog), /Reasoning effort/);
});

test('invalid tool arguments, unsupported fields and iteration limits cannot alter a draft', () => {
  for (const patch of [null, [], 'text', { name: '' }, { name: 12 }, { max_iterations: 0 }, { max_iterations: 241 }, { max_iterations: 1.5 }, { tool_timeout: -1 }, { skills: [null] }, { scope: 'global' }, { connections: {} }, { shared_with_all_workspaces: true }, { id: 'other-agent' }, { config: {} }]) {
    assert.throws(() => applyAgentDraftPatch(draft(), patch, catalog));
  }
  assert.equal(applyAgentDraftPatch(draft(), { max_iterations: 240 }, catalog).max_iterations, 240);
});

const chatSource = await readFile(new URL('../src/lib/helper/chat.ts', import.meta.url), 'utf8');
const chatCode = ts.transpileModule(chatSource.replace("import { authFetch as fetch } from '../api/transport';", 'const fetch = (...args) => globalThis.agentBuilderFetch(...args);'), { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { streamChatCompletion } = await import(`data:text/javascript;base64,${Buffer.from(chatCode).toString('base64')}`);
const toolChunk = { choices: [{ delta: { tool_calls: [{ index: 0, id: 'call-1', function: { name: 'update_agent_form', arguments: '{"name":"Reviewer"}' } }] } }] };

test('form tools are delivered only after a complete successful stream', async () => {
  for (const [name, ending, succeeds] of [
    ['complete', { choices: [{ delta: {}, finish_reason: 'tool_calls' }] }, true],
    ['stop-compatible', { choices: [{ delta: {}, finish_reason: 'stop' }] }, true],
    ['truncated', { choices: [{ delta: {}, finish_reason: 'length' }] }, false],
    ['filtered', { choices: [{ delta: {}, finish_reason: 'content_filter' }] }, false],
    ['upstream-error', { error: { message: 'Upstream failed' } }, false],
    ['early-eof', null, false],
  ]) {
    let calls = [];
    globalThis.agentBuilderFetch = async () => new Response([toolChunk, ending].filter(Boolean).map(c => `data: ${JSON.stringify(c)}\n\n`).join(''));
    const request = streamChatCompletion('api/v1/chat/completions', { model: 'p/m', messages: [], stream: true }, { requireComplete: true, onDelta() {}, onError() {}, onToolCalls(value) { calls = value; } }, new AbortController().signal);
    if (succeeds) { await request; assert.equal(calls.length, 1, name); }
    else { await assert.rejects(request, undefined, name); assert.deepEqual(calls, [], name); }
  }
  delete globalThis.agentBuilderFetch;
});

test('cancellation cannot apply tool calls even if the transport finishes late', async () => {
  const controller = new AbortController();
  let applied = false;
  globalThis.agentBuilderFetch = async () => {
    controller.abort();
    return new Response([toolChunk, { choices: [{ delta: {}, finish_reason: 'tool_calls' }] }].map(c => `data: ${JSON.stringify(c)}\n\n`).join(''));
  };
  await assert.rejects(streamChatCompletion('api/v1/chat/completions', { model: 'p/m', messages: [], stream: true }, { requireComplete: true, onDelta() {}, onError() {}, onToolCalls() { applied = true; } }, controller.signal), { name: 'AbortError' });
  assert.equal(applied, false);
  delete globalThis.agentBuilderFetch;
});

test('existing chat callers retain their non-strict streaming contract', async () => {
  let delivered = false;
  globalThis.agentBuilderFetch = async () => new Response(`data: ${JSON.stringify(toolChunk)}\n\n`);
  await streamChatCompletion('api/v1/chat/completions', { model: 'p/m', messages: [], stream: true }, { onDelta() {}, onError() {}, onToolCalls() { delivered = true; } }, new AbortController().signal);
  assert.equal(delivered, true);
  delete globalThis.agentBuilderFetch;
});
