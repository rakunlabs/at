import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';

const css = readFileSync(new URL('../src/style/global.css', import.meta.url), 'utf8');

test('grain is scoped to backgrounds and navigation, not generic fills', () => {
  const selector = css.match(/([^{}]+)\{\s*background-image: var\(--grain\);/)[1];
  for (const surface of ['html', 'body', '.grain-background', '.app-sidebar', '.app-navbar']) {
    assert.ok(selector.includes(surface));
  }
  assert.ok(!selector.includes('.bg-dark-base'));
  assert.ok(!selector.includes('.oc-theme'));
  assert.match(css, /@media \(prefers-contrast: more\)\s*\{[^}]+background-image: none;/);
});

test('boxed content masks grain without overriding explicit background utilities', () => {
  assert.match(css, /@layer base\s*\{\s*:where\(\.border, \.border-2, \.settings-section, \.settings-button\)\s*\{\s*background-color: var\(--color-dark-base\);/);
  const app = readFileSync(new URL('../src/App.svelte', import.meta.url), 'utf8');
  assert.match(app, /grain-background grid h-full w-full/);
});
