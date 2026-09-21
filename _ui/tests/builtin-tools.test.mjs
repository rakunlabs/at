import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/builtin-tools.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { builtinDisabledBy } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('cached saved or inherited definitions cannot override a disabled ancestor', () => {
  const tool = { name: 'file_read', family: 'builtin_other', group: 'files' };
  for (const disabled of ['builtin_tools', 'builtin_other', 'files']) {
    assert.equal(builtinDisabledBy(tool, key => key !== disabled), disabled);
  }
  assert.equal(builtinDisabledBy(tool, () => true), '');
});

test('server refusal is retained before feature catalog arrives', () => {
  assert.equal(builtinDisabledBy({ name: 'bash_execute', disabled_by: 'builtin_shell' }, () => true), 'builtin_shell');
  assert.equal(builtinDisabledBy({ name: 'todo_read' }, key => key !== 'builtin_other'), 'builtin_other');
  assert.equal(builtinDisabledBy({ name: 'http_request' }, key => key !== 'builtin_other'), '');
});
