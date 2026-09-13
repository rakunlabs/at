import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

globalThis.fileWorkspace = { selected: 'workspace-a' };
const source = await readFile(new URL('../src/lib/api/files.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source
  .replace("import axios from 'axios';", 'const axios = { create: () => ({}) };')
  .replace("import { workspaceTransport } from './transport';", 'const workspaceTransport = globalThis.fileWorkspace;'), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const { fileServeUrl } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('native file URLs pin a nonsecret workspace and preserve base path and filename encoding', () => {
  const value = fileServeUrl('assets/a & b/clip #1.mp4', '2026-09-14 12:00:00');
  const url = new URL(value, 'https://at.example/at/');
  assert.equal(url.pathname, '/at/api/v1/files/serve');
  assert.equal(url.searchParams.get('workspace_id'), 'workspace-a');
  assert.equal(url.searchParams.get('path'), 'assets/a & b/clip #1.mp4');
  assert.equal(url.searchParams.get('v'), '2026-09-14 12:00:00');
  globalThis.fileWorkspace.selected = 'workspace-b';
  assert.equal(url.searchParams.get('workspace_id'), 'workspace-a');
  assert.equal(new URL(fileServeUrl('new.mp4'), 'https://at.example/').searchParams.get('workspace_id'), 'workspace-b');
  globalThis.fileWorkspace.selected = '';
  assert.equal(new URL(fileServeUrl('new.mp4'), 'https://at.example/').searchParams.has('workspace_id'), false);
});
