import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/tool-activity.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { toolResultsByMessage, formatToolPayload, toolResultFailed } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
const call = id => ({ id, type: 'function', function: { name: 'file_read', arguments: '{}' } });

test('same-name calls keep distinct results, including out-of-order results', () => {
  const results = toolResultsByMessage([
    { role: 'assistant', content: '', tool_calls: [call('a'), call('b')] },
    { role: 'tool', tool_call_id: 'b', content: 'Second file' },
    { role: 'tool', tool_call_id: 'a', content: 'First file' },
  ]);
  assert.equal(results.get(0).get('a').content, 'First file');
  assert.equal(results.get(0).get('b').content, 'Second file');
});

test('reused IDs in a later turn cannot overwrite an earlier result', () => {
  const results = toolResultsByMessage([
    { role: 'assistant', content: '', tool_calls: [call('a')] },
    { role: 'tool', tool_call_id: 'a', content: '' },
    { role: 'user', content: 'Try again' },
    { role: 'assistant', content: '', tool_calls: [call('a')] },
    { role: 'tool', tool_call_id: 'a', content: 'New result' },
  ]);
  assert.equal(results.get(0).get('a').content, '');
  assert.equal(results.get(3).get('a').content, 'New result');
});

test('missing and orphaned results are not fabricated or attached to another call', () => {
  const results = toolResultsByMessage([
    { role: 'tool', tool_call_id: 'a', content: 'Older page' },
    { role: 'assistant', content: '', tool_calls: [call('a')] },
    { role: 'user', content: 'Interrupted' },
    { role: 'tool', tool_call_id: 'a', content: 'Unrelated' },
  ]);
  assert.equal(results.get(1).size, 0);
});

test('JSON is readable while plaintext, empty results and errors retain their meaning', () => {
  assert.equal(formatToolPayload('{"path":"a"}'), '{\n  "path": "a"\n}');
  assert.equal(formatToolPayload('line 1\nline 2'), 'line 1\nline 2');
  assert.equal(formatToolPayload(''), '');
  assert.equal(toolResultFailed('Error: tool failed'), true);
  assert.equal(toolResultFailed('{"isError":true}'), true);
  assert.equal(toolResultFailed('{"error":"denied"}'), true);
  assert.equal(toolResultFailed('{"error":null,"result":"ok"}'), false);
  assert.equal(toolResultFailed('No errors found'), false);
});
