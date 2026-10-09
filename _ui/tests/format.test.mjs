import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const f = await import(await moduleURL(new URL('../src/lib/helper/format.ts', import.meta.url)));

test('RFC3339 timestamps use the browser offset, including date boundaries and DST', () => {
  const previous = process.env.TZ;
  try {
    for (const zone of ['UTC', 'Europe/Istanbul', 'America/New_York', 'Asia/Kathmandu']) {
      process.env.TZ = zone;
      for (const iso of ['2026-10-09T23:30:45Z', '2026-01-01T00:15:45Z', '2026-03-08T06:59:00Z', '2026-03-08T07:01:00Z']) {
        const date = new Date(iso);
        const options = { year: 'numeric', month: 'short', day: 'numeric' };
        const expected = f.formatDateTime(iso);
        assert.match(expected, /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[+-]\d{2}:\d{2}$/);
        assert.equal(Date.parse(expected), date.getTime(), `${zone}: ${iso}`);
        assert.equal(expected.slice(0, 16), f.formatDateTimeInput(date));
        assert.equal(f.formatDate(iso, true), expected);
        assert.equal(f.formatDateTime(date), expected);
        assert.equal(f.formatDate(iso), date.toLocaleDateString(undefined, options));
        assert.equal(f.formatTime(iso), date.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' }));
        assert.equal(f.formatLocalDateTime(iso), expected);
      }
    }
  } finally {
    if (previous === undefined) delete process.env.TZ;
    else process.env.TZ = previous;
  }
});

test('RFC3339 offsets include non-hour zones and change with DST; fractions are preserved', () => {
  const previous = process.env.TZ;
  try {
    process.env.TZ = 'Europe/Istanbul';
    assert.equal(f.formatDateTime('2026-10-09T23:30:45Z'), '2026-10-10T02:30:45+03:00');
    process.env.TZ = 'Asia/Kathmandu';
    assert.equal(f.formatDateTime('2026-10-09T23:30:45.123Z'), '2026-10-10T05:15:45.123+05:45');
    process.env.TZ = 'America/New_York';
    assert.equal(f.formatDateTime('2026-03-08T06:59:00Z'), '2026-03-08T01:59:00-05:00');
    assert.equal(f.formatDateTime('2026-03-08T07:01:00Z'), '2026-03-08T03:01:00-04:00');
    process.env.TZ = 'UTC';
    assert.equal(f.formatDateTime('2026-10-09T23:30:45Z'), '2026-10-09T23:30:45+00:00');
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
    assert.equal(f.formatUTCDateTime(value), '');
  }
  assert.equal(f.formatDateTime('invalid'), 'Invalid Date');
  assert.equal(f.formatDateTimeInput('invalid'), '');
  assert.equal(f.formatLocalDateTime('invalid'), '');
  assert.equal(f.formatUTCDateTime('invalid'), '');
});

test('UTC titles show the same instant independently of the browser timezone', () => {
  const previous = process.env.TZ;
  try {
    for (const zone of ['Europe/Istanbul', 'America/New_York']) {
      process.env.TZ = zone;
      assert.equal(f.formatUTCDateTime('2026-10-09T18:30:00+03:00'), '2026-10-09T15:30:00.000Z (UTC)');
      assert.equal(f.formatUTCDateTime(new Date('2026-10-09T15:30:00.123Z')), '2026-10-09T15:30:00.123Z (UTC)');
    }
  } finally {
    if (previous === undefined) delete process.env.TZ;
    else process.env.TZ = previous;
  }
});

test('build dates and token expiry inputs use the local formatters; budget displays do not force a zone', async () => {
  const source = path => readFile(new URL(`../src/${path}`, import.meta.url), 'utf8');
  assert.match(await source('pages/Settings.svelte'), /formatLocalDateTime\(storeInfo\.build_date\)/);
  assert.match(await source('pages/Settings.svelte'), /title=\{label === 'Build date' \? formatUTCDateTime\(storeInfo\.build_date\)/);
  assert.match(await source('pages/Tokens.svelte'), /editExpiresAt = formatDateTimeInput\(token\.expires_at\)/);
  for (const page of ['Agents', 'Usage', 'OrganizationDetail']) {
    assert.doesNotMatch(await source(`pages/${page}.svelte`), /timeZone:/);
    assert.match(await source(`pages/${page}.svelte`), /title=\{formatUTCDateTime\(/);
  }
});
