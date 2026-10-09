import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/model-pricing.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { priceFormFromConfig, priceConfigFromForm, validatePriceForm, setPriceField } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('round-trips stored prices, keeping an explicit free model', () => {
  const stored = { a: { input: 3, output: 15, cache_read: 0.3 }, free: { input: 0, output: 0 } };
  const form = priceFormFromConfig(stored);
  assert.deepEqual(priceConfigFromForm(['a', 'free'], form), stored);
});

test('empty rows and removed models are not sent', () => {
  let form = setPriceField({}, 'a', 'input', '');
  form = setPriceField(form, 'gone', 'input', '1');
  form = setPriceField(form, 'gone', 'output', '1');
  assert.equal(priceConfigFromForm(['a'], form), undefined);
});

test('validation requires input and output and rejects negatives', () => {
  let form = setPriceField({}, 'a', 'input', '1');
  assert.match(validatePriceForm(['a'], form), /both input and output/);
  form = setPriceField(form, 'a', 'output', '-2');
  assert.match(validatePriceForm(['a'], form), /non-negative/);
  form = setPriceField(form, 'a', 'output', '2');
  assert.equal(validatePriceForm(['a'], form), '');
});
