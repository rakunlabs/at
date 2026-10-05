import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const page = await readFile(new URL('../src/pages/Providers.svelte', import.meta.url), 'utf8');
function preset(id) {
  const start = page.indexOf(`id: '${id}'`);
  assert.notEqual(start, -1, `missing preset ${id}`);
  const end = page.indexOf('\n      id:', start + 1);
  return page.slice(start, end === -1 ? undefined : end);
}

test('Groq does not seed retired Llama and Mixtral IDs', () => {
  const groq = preset('groq');
  assert.match(groq, /model: 'openai\/gpt-oss-120b'/);
  assert.doesNotMatch(groq, /llama-3\.3-70b-versatile|llama-3\.1-8b-instant|mixtral-8x7b-32768/);
});

test('Cerebras reuses the OpenAI adapter and its published endpoint', () => {
  const cerebras = preset('cerebras');
  assert.match(cerebras, /type: 'openai'/);
  assert.match(cerebras, /https:\/\/api\.cerebras\.ai\/v1\/chat\/completions/);
  assert.match(cerebras, /model: 'gpt-oss-120b'/);
});

test('DeepSeek and Cohere include current thinking models', () => {
  assert.match(preset('deepseek'), /model: 'deepseek-flash'/);
  assert.match(preset('deepseek'), /deepseek-v4-pro/);
  assert.doesNotMatch(preset('deepseek'), /DeepSeek-R1|DeepSeek-V3/);
  assert.match(preset('cohere'), /command-a-reasoning-08-2025/);
});
