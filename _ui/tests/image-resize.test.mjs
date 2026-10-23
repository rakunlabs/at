import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';

const { targetImageSize, attachableFile, IMAGE_MAX_EDGE } = await import(await moduleURL(new URL('../src/lib/helper/image-resize.ts', import.meta.url)));

test('images within the limits keep their size', () => {
  assert.equal(targetImageSize(1200, 800, 500_000), null);
  assert.equal(targetImageSize(0, 0, 1), null);
});

test('large images scale to the longest edge, keeping the aspect ratio', () => {
  assert.deepEqual(targetImageSize(4000, 3000, 1_000_000), { width: IMAGE_MAX_EDGE, height: 1500 });
  assert.deepEqual(targetImageSize(1000, 6000, 1_000_000), { width: 333, height: IMAGE_MAX_EDGE });
  // Small in pixels but heavy in bytes: re-encoded at the same size.
  assert.deepEqual(targetImageSize(1800, 1200, 9_000_000), { width: 1800, height: 1200 });
});

test('files that cannot be resized here are attached unchanged', async () => {
  const gif = new File([new Uint8Array(10)], 'a.gif', { type: 'image/gif' });
  assert.equal(await attachableFile(gif), gif);
  const pdf = new File([new Uint8Array(10)], 'a.pdf', { type: 'application/pdf' });
  assert.equal(await attachableFile(pdf), pdf);
});
