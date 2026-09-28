import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const page = await readFile(new URL('../src/pages/Mcps.svelte', import.meta.url), 'utf8');
const types = await readFile(new URL('../src/lib/api/mcp-servers.ts', import.meta.url), 'utf8');

test('migrated skill tools are typed and visible in the MCP list and editor', () => {
  assert.match(types, /inline_tools\?: MCPInlineTool\[\]/);
  assert.match(page, /Migrated skill tools/);
  assert.match(page, /set\.config\?\.inline_tools/);
  assert.match(page, /skill tools<\/span>/);
});

test('editing an MCP preserves migrated and otherwise hidden config', () => {
  assert.match(page, /preservedConfig = JSON\.parse\(JSON\.stringify\(cfg\)\)/);
  assert.match(page, /config:\s*\{\s*\.\.\.preservedConfig,/);
  assert.match(page, /inline_tools: formInlineTools/);
});
