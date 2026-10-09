import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const f = await import(await moduleURL(new URL('../src/lib/helper/format.ts', import.meta.url)));

test('display timestamps use the browser timezone and locale, including date boundaries and DST', () => {
  const previous = process.env.TZ;
  try {
    for (const zone of ['UTC', 'Europe/Istanbul', 'America/New_York', 'Asia/Kathmandu']) {
      process.env.TZ = zone;
      for (const iso of ['2026-10-09T23:30:45Z', '2026-01-01T00:15:45Z', '2026-03-08T06:59:00Z', '2026-03-08T07:01:00Z']) {
        const date = new Date(iso);
        const options = { year: 'numeric', month: 'short', day: 'numeric' };
        const expected = date.toLocaleString(undefined, { ...options, hour: '2-digit', minute: '2-digit', second: '2-digit' });
        assert.equal(f.formatDateTime(iso), expected, `${zone}: ${iso}`);
        assert.equal(f.formatDate(iso, true), expected);
        assert.equal(f.formatDateTime(date), expected);
        assert.equal(f.formatDate(iso), date.toLocaleDateString(undefined, options));
        assert.equal(f.formatTime(iso), date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' }));
        assert.equal(f.formatLocalDateTime(iso), date.toLocaleString());
      }
    }
  } finally {
    if (previous === undefined) delete process.env.TZ;
    else process.env.TZ = previous;
  }
});

test('datetime-local inputs show local wall-clock time, not UTC', () => {
  const previous = process.env.TZ;
  try {
    process.env.TZ = 'Europe/Istanbul';
    assert.equal(f.formatDateTimeInput('2026-10-09T23:30:00Z'), '2026-10-10T02:30');
    process.env.TZ = 'America/New_York';
    assert.equal(f.formatDateTimeInput('2026-01-01T00:15:00Z'), '2025-12-31T19:15');
    assert.equal(f.formatDateTimeInput('2026-03-08T06:59:00Z'), '2026-03-08T01:59');
    assert.equal(f.formatDateTimeInput('2026-03-08T07:01:00Z'), '2026-03-08T03:01');
  } finally {
    if (previous === undefined) delete process.env.TZ;
    else process.env.TZ = previous;
  }
});

test('missing and invalid timestamps keep explicit fallbacks', () => {
  for (const value of [null, undefined, '']) {
    assert.equal(f.formatDateTime(value), 'N/A');
    assert.equal(f.formatTime(value), '-');
    assert.equal(f.formatDateTimeInput(value), '');
    assert.equal(f.formatLocalDateTime(value), '');
  }
  assert.equal(f.formatDateTime('invalid'), 'Invalid Date');
  assert.equal(f.formatDateTimeInput('invalid'), '');
  assert.equal(f.formatLocalDateTime('invalid'), '');
});

test('build dates and token expiry inputs use the local formatters; budget displays do not force a zone', async () => {
  const source = path => readFile(new URL(`../src/${path}`, import.meta.url), 'utf8');
  assert.match(await source('pages/Settings.svelte'), /formatLocalDateTime\(storeInfo\.build_date\)/);
  assert.match(await source('pages/Tokens.svelte'), /editExpiresAt = formatDateTimeInput\(token\.expires_at\)/);
  for (const page of ['Agents', 'Usage', 'OrganizationDetail']) {
    assert.doesNotMatch(await source(`pages/${page}.svelte`), /timeZone:/);
  }
});
