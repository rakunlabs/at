import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/workflow/ports.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { canvasInputHandle, storedInputHandle } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('bidirectional data nodes use distinct canvas handles without changing saved graphs', () => {
  for (const kind of ['edit_fields', 'filter', 'aggregate', 'wait']) {
    const canvas = canvasInputHandle(kind, 'data');
    assert.notEqual(canvas, 'data', `${kind} input must not overwrite its output handle`);
    assert.equal(storedInputHandle(kind, canvas), 'data');
    assert.equal(canvasInputHandle(kind, canvas), canvas, 'already-normalized AI tool arguments work');
    assert.equal(storedInputHandle(kind, 'data'), 'data', 'old saved edges remain compatible');
  }
});

test('unambiguous and dynamic handles retain their original IDs', () => {
  for (const [kind, handle] of [['script', 'data'], ['script', 'data2'], ['merge', 'left'], ['merge', 'right'], ['switch', 'data'], ['output', 'input'], ['agent_call', 'prompt']]) {
    assert.equal(canvasInputHandle(kind, handle), handle);
    assert.equal(storedInputHandle(kind, handle), handle);
  }
});
