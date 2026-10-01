import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');

const helperSource = await readFile(new URL('../src/lib/helper/chat-tool-selections.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(helperSource, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { initialWorkbenchSetup, newWorkbenchSetup, normalizeWorkbenchSetup, workbenchSetupsEqual } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('shipped setup enables whoami and copies browser defaults', () => {
  const frontend = ['question'];
  const setup = initialWorkbenchSetup(frontend);
  assert.deepEqual(setup.builtin_tools, ['whoami']);
  assert.deepEqual(setup.frontend_tools, frontend);
  setup.frontend_tools.push('todo_read');
  assert.deepEqual(frontend, ['question']);
});

test('normalization preserves explicit none, migrates todos and copies selections', () => {
  const input = { model: 'p/m', reasoning_effort: 'high', system_prompt: 'Prompt', mcp_sets: ['ops', 'ops'], skills: ['docs'], builtin_tools: ['todo_read', 'whoami', 'whoami'], frontend_tools: [] };
  const setup = normalizeWorkbenchSetup(input, ['question']);
  assert.deepEqual(setup, { ...input, mcp_sets: ['ops'], builtin_tools: ['whoami'], frontend_tools: ['todo_read'] });
  assert.deepEqual(normalizeWorkbenchSetup({ builtin_tools: [], frontend_tools: [] }, ['question']).frontend_tools, []);
  assert.deepEqual(normalizeWorkbenchSetup({}, ['question']).builtin_tools, []);
  assert.deepEqual(normalizeWorkbenchSetup({}, ['question']).frontend_tools, ['question']);
  setup.skills.push('another');
  assert.deepEqual(input.skills, ['docs']);
  assert.equal(workbenchSetupsEqual(setup, normalizeWorkbenchSetup(setup)), true);
});

test('foreign config values cannot become prompts or tool selections', () => {
  const setup = normalizeWorkbenchSetup({ model: false, reasoning_effort: 3, system_prompt: {}, skills: 'docs', mcp_sets: [null, 'ops', 3], builtin_tools: ['whoami', false], frontend_tools: {} });
  assert.deepEqual(setup, { model: '', reasoning_effort: '', system_prompt: '', skills: [], mcp_sets: ['ops'], builtin_tools: ['whoami'], frontend_tools: [] });
});

test('new chat restores the entire account setup without re-enabling disabled tools', () => {
  const defaults = normalizeWorkbenchSetup({ model: 'p/m', reasoning_effort: 'high', system_prompt: 'Saved prompt', skills: ['docs'], builtin_tools: [], frontend_tools: [] });
  const next = newWorkbenchSetup(defaults, ['p/other', 'p/m']);
  assert.deepEqual(next, defaults);
  next.skills.push('changed');
  assert.deepEqual(defaults.skills, ['docs']);
  assert.equal(newWorkbenchSetup(defaults, ['p/other']).model, 'p/other');
  assert.equal(newWorkbenchSetup(defaults, []).model, '');
});

test('preset equality ignores selection order but detects every setup field', () => {
  const setup = normalizeWorkbenchSetup({ model: 'p/m', reasoning_effort: 'high', system_prompt: 'Prompt', skills: ['one', 'two'], mcp_sets: ['ops'], builtin_tools: ['whoami'], frontend_tools: ['question'] });
  assert.equal(workbenchSetupsEqual(setup, { ...setup, skills: ['two', 'one'] }), true);
  for (const field of ['model', 'reasoning_effort', 'system_prompt']) {
    assert.equal(workbenchSetupsEqual(setup, { ...setup, [field]: 'different' }), false, field);
  }
  for (const field of ['skills', 'mcp_sets', 'builtin_tools', 'frontend_tools']) {
    assert.equal(workbenchSetupsEqual(setup, { ...setup, [field]: [] }), false, field);
  }
});

test('Chats configures documentation skills directly without an agent binding', () => {
  assert.doesNotMatch(source, /agent_id|boundAgent|Choose an agent/);
  assert.match(source, /type WorkbenchTab = 'prompt' \| 'skills' \| 'tools' \| 'chat'/);
  assert.match(source, /\{ id: 'skills', label: 'Skills' \}/);
  assert.match(source, /Add reusable Markdown instructions and reference resources\. Skills do not grant tools\./);
  assert.match(source, /skills: \[\.\.\.selectedSkillNames\]/);
});

test('Workbench is top-aligned and grows downward within the viewport', () => {
  assert.match(source, /fixed inset-0[^"\n]*items-start[^"\n]*overflow-y-auto/);
  assert.match(source, /max-h-\[calc\(100dvh-2rem\)\]/);
});
