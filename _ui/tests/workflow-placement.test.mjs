import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/workflow/node-placement.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { findNodePlacement } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('new cards avoid a chain of overlapping cards without moving existing content', () => {
  const nodes = [
    { x: 0, y: 0, width: 256, height: 100 },
    { x: 0, y: 150, width: 256, height: 200 },
    { x: 1000, y: 390, width: 256, height: 100 },
  ];
  const before = structuredClone(nodes);
  const position = findNodePlacement({ x: 20, y: 20 }, { width: 256, height: 140 }, nodes);
  assert.deepEqual(position, { x: 20, y: 390 });
  assert.deepEqual(nodes, before);
});

test('panned canvas coordinates and a large annotation size are respected', () => {
  const preferred = { x: -600, y: -400 };
  assert.deepEqual(findNodePlacement(preferred, { width: 250, height: 200 }, []), preferred);
  const occupied = [{ x: -400, y: -300, width: 256, height: 100 }];
  const result = findNodePlacement(preferred, { width: 250, height: 200 }, occupied);
  assert.deepEqual(result, { x: -600, y: -160 });
  assert.deepEqual(preferred, { x: -600, y: -400 });
});
