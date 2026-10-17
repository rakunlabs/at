import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const { cronRunTime, cronRemaining } = await import(await moduleURL(new URL('../src/lib/helper/cron-time.ts', import.meta.url)));

test('cron times retain offsets and support the configured timezone', () => {
  assert.equal(cronRunTime('2026-03-08T09:00:00+03:00'), '2026-03-08 09:00+03:00');
  assert.equal(cronRunTime('2026-03-08T06:00:00Z'), '2026-03-08 06:00 UTC');
  assert.match(cronRunTime('2026-03-08T06:00:00Z', 'Europe/Istanbul'), /09:00.*GMT\+3/);
});

test('countdowns cover near, far and stale occurrences', () => {
  const now = Date.parse('2026-03-08T06:00:00Z');
  for (const [minutes, expected] of [[-1, 'Refreshing…'], [0.5, 'Less than a minute left'], [8, '8m left'], [90, '1h 30m left'], [1500, '1d 1h left']]) {
    assert.equal(cronRemaining(new Date(now + minutes * 60000).toISOString(), now), expected);
  }
  assert.equal(cronRemaining('invalid', now), 'Unavailable');
});
