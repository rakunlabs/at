import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const src = await readFile(new URL('../src/lib/helper/free-tier-setup.ts', import.meta.url), 'utf8');
const js = ts.transpileModule(src, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { freeTierModels, freeTierConfig, freeTierTargets, canAdoptFreeTierProfile, uncertainTokenCreation } = await import(`data:text/javascript;base64,${Buffer.from(js).toString('base64')}`);
const entry = { id: 'groq', type: 'openai', name: 'Groq', base_url: 'https://api.groq.com/openai/v1/chat/completions' };
const draft = { entry, key: 'key', models: 'a, b\na', quota: { requests_per_day: 10 }, created_reference: 'provider:alice' };

test('models deduplicate in order, no inferred prices or models', () => {
  assert.deepEqual(freeTierModels('a, b\na'), ['a', 'b']);
  const cfg = freeTierConfig(draft);
  assert.deepEqual(cfg.models, ['a', 'b']);
  assert.equal(cfg.free_tier, 'groq');
  assert.equal(cfg.model_pricing, undefined);
  assert.equal(cfg.quota.requests_per_day, 10);
});
test('no paid fallback is implicit; explicit fallback is last', () => {
  assert.deepEqual(freeTierTargets([draft], ''), ['provider:alice/a', 'provider:alice/b']);
  assert.deepEqual(freeTierTargets([draft], 'paid/m'), ['provider:alice/a', 'provider:alice/b', 'paid/m']);
});
test('own key and chosen models are mandatory', () => {
  assert.throws(() => freeTierConfig({ ...draft, key: '' }), /own API key/);
  assert.throws(() => freeTierConfig({ ...draft, models: '' }), /Choose a model/);
});
test('OpenRouter setup cannot accidentally choose a paid model', () => {
  assert.throws(() => freeTierConfig({ ...draft, entry: { ...entry, id: 'openrouter' }, models: 'paid/model' }), /only/);
  assert.equal(freeTierConfig({ ...draft, entry: { ...entry, id: 'openrouter' }, models: 'openrouter/free' }).model, 'openrouter/free');
});
test('recovery never adopts a strategy that can put paid fallback first', () => {
  const targets = ['free/m', 'paid/m'];
  const profile = { owner_user_id: 'me', strategy: 'priority', targets };
  assert.equal(canAdoptFreeTierProfile(profile, targets), true);
  for (const strategy of ['weighted', 'round_robin', 'least_cost', 'sticky', '']) {
    assert.equal(canAdoptFreeTierProfile({ ...profile, strategy }, targets), false);
  }
  assert.equal(canAdoptFreeTierProfile({ ...profile, owner_user_id: '' }, targets), false);
  assert.equal(canAdoptFreeTierProfile(profile, ['paid/m', 'free/m']), false);
});
test('lost or proxy/server-error token responses cannot be blindly recreated', () => {
  for (const status of [undefined, 0, 408, 500, 502, 503, 504]) assert.equal(uncertainTokenCreation(status), true);
  for (const status of [400, 401, 403, 409, 429]) assert.equal(uncertainTokenCreation(status), false);
});
test('provider editor preserves quotas and provenance; wizard never retries or persists keys', async () => {
  const page = await readFile(new URL('../src/pages/Providers.svelte', import.meta.url), 'utf8');
  assert.match(page, /formQuota = structuredClone/);
  assert.match(page, /quota: formQuota/);
  assert.match(page, /free_tier: formFreeTier/);
  const wizard = await readFile(new URL('../src/lib/components/FreeTierSetup.svelte', import.meta.url), 'utf8');
  assert.match(wizard, /allowed_providers_mode: 'none', allowed_models_mode: 'list'/);
  assert.match(wizard, /scope: 'personal'/);
  assert.match(wizard, /draft\.key = ''/);
  assert.doesNotMatch(wizard, /localStorage\.(setItem|getItem)|sessionStorage\./);
});
