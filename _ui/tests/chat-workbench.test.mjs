import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const source = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');

test('Chats configures skills directly without an agent binding', () => {
  assert.doesNotMatch(source, /agent_id|boundAgent|Choose an agent/);
  assert.match(source, /type WorkbenchTab = 'prompt' \| 'skills' \| 'tools' \| 'chat'/);
  assert.match(source, /\{ id: 'skills', label: 'Skills' \}/);
  assert.match(source, /Add reusable instructions and tools directly to this conversation/);
  assert.match(source, /skills: \[\.\.\.selectedSkillNames\]/);
});

test('Workbench is top-aligned and grows downward within the viewport', () => {
  assert.match(source, /fixed inset-0[^"\n]*items-start[^"\n]*overflow-y-auto/);
  assert.match(source, /max-h-\[calc\(100dvh-2rem\)\]/);
});
