import assert from 'node:assert/strict';
import { test } from 'node:test';
import { moduleURL } from './typescript-module.mjs';
const { createChatMediaCache } = await import(await moduleURL(new URL('../src/lib/helper/chat-media-cache.ts', import.meta.url)));

test('downloads are coalesced and failures can be retried', async () => {
  let reads = 0;
  const cache = createChatMediaCache({ download: async () => { if (++reads === 1) throw new Error('offline'); return 'bytes'; }, upload: async () => ({ id: 'saved' }) });
  const first = cache.download('image');
  assert.equal(first, cache.download('image'));
  await assert.rejects(first, /offline/);
  assert.equal(await cache.download('image'), 'bytes');
  assert.equal(await cache.download('image'), 'bytes');
  assert.equal(reads, 2);
});

test('uploads share an in-flight request and seed downloaded bytes for model replay', async () => {
  let uploads = 0, downloads = 0;
  const cache = createChatMediaCache({ download: async () => { downloads++; return 'unexpected'; }, upload: async () => { uploads++; return { id: 'saved' }; } });
  const first = cache.upload('data:image/png;base64,AAAA', 'image.png');
  assert.equal(first, cache.upload('data:image/png;base64,AAAA', 'image.png'));
  assert.equal(await first, 'saved');
  assert.equal(await cache.upload('data:image/png;base64,AAAA', 'image.png'), 'saved');
  assert.equal(await cache.download('saved'), 'data:image/png;base64,AAAA');
  assert.equal(uploads, 1); assert.equal(downloads, 0);
});

test('failed uploads retry and caches are never shared between workbenches', async () => {
  let uploads = 0;
  const options = { download: async () => 'bytes', upload: async () => { if (++uploads === 1) throw new Error('offline'); return { id: 'saved' }; } };
  const cache = createChatMediaCache(options);
  await assert.rejects(cache.upload('bytes', 'file'), /offline/);
  assert.equal(await cache.upload('bytes', 'file'), 'saved');
  assert.equal(await createChatMediaCache(options).upload('bytes', 'file'), 'saved');
  assert.equal(uploads, 3);
});
