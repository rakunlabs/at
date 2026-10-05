import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

async function load(path) {
  const source = await readFile(new URL(path, import.meta.url), 'utf8');
  const code = ts.transpileModule(source, {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText;

  return import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
}

const { imageGenerationConfig, imageGenerationForm } = await load('../src/lib/helper/image-generation.ts');

test('form round-trips stored settings', () => {
  const form = imageGenerationForm({ provider: 'openai', quality: 'high' });
  assert.deepEqual(form, { provider: 'openai', model: '', size: '', quality: 'high', background: '' });
  assert.deepEqual(imageGenerationConfig(form, ['generate_image']), { provider: 'openai', quality: 'high' });
});

test('nothing is stored when the tool is off or the form is empty', () => {
  const form = imageGenerationForm({ provider: 'openai' });
  assert.equal(imageGenerationConfig(form, ['bash_execute']), undefined);
  assert.equal(imageGenerationConfig(imageGenerationForm(), ['generate_image']), undefined);
});

test('values are trimmed and blanks dropped', () => {
  const form = { ...imageGenerationForm(), provider: '  mm ', model: '   ' };
  assert.deepEqual(imageGenerationConfig(form, ['generate_image']), { provider: 'mm' });
});
