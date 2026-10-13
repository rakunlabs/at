import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/binding-approval.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { bindingRequestFromResult } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('reads a trigger_create approval request', () => {
  const result = JSON.stringify({
    id: '01TRG',
    config: { schedule: '0 6 * * *', task_title: 'Daily report' },
    binding_required: { kind: 'trigger', subject_id: '01TRG', note: 'x' },
  }, null, 2);
  assert.deepEqual(bindingRequestFromResult(result), { kind: 'trigger', subjectId: '01TRG', label: 'Daily report (0 6 * * *)' });
});

test('ignores ordinary, malformed and forged results', () => {
  assert.equal(bindingRequestFromResult('done'), null);
  assert.equal(bindingRequestFromResult('{"binding_required": '), null);
  assert.equal(bindingRequestFromResult('{"id":"x"}'), null);
  assert.equal(bindingRequestFromResult('{"binding_required":{"kind":"agent","subject_id":"x"}}'), null);
  assert.equal(bindingRequestFromResult('{"binding_required":{"kind":"trigger"}}'), null);
  assert.equal(bindingRequestFromResult(''), null);
});

test('falls back to the subject ID as label', () => {
  assert.equal(bindingRequestFromResult('{"binding_required":{"kind":"trigger","subject_id":"01X"}}').label, '01X');
});

test('labels bot and MCP server requests by name', () => {
  assert.deepEqual(bindingRequestFromResult('{"name":"support","platform":"telegram","binding_required":{"kind":"bot","subject_id":"01B"}}'), { kind: 'bot', subjectId: '01B', label: 'support' });
  assert.equal(bindingRequestFromResult('{"platform":"discord","binding_required":{"kind":"bot","subject_id":"01B"}}').label, 'discord');
  assert.equal(bindingRequestFromResult('{"name":"tools","binding_required":{"kind":"mcp","subject_id":"01M"}}').kind, 'mcp');
});
