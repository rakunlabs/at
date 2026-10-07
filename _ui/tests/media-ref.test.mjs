import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/media-ref.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { mediaRefIDs, resolveMediaRefs, messageText } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

test('collects image and link references', () => {
  const ids = mediaRefIDs('Here: ![cat](media:01ABC) and [report](media:02DEF "title"), not media:03GHI.');
  assert.deepEqual([...ids], ['01ABC', '02DEF']);
  assert.equal(mediaRefIDs('no refs').size, 0);
});

test('resolves references to scoped URLs and keeps the label when unavailable', () => {
  const url = id => `api/v1/media/${id}?workspace_id=w`;
  assert.equal(resolveMediaRefs('![cat](media:01ABC)', url), '![cat](<api/v1/media/01ABC?workspace_id=w>)');
  assert.equal(resolveMediaRefs('[r](<media:02DEF>)', url), '[r](<api/v1/media/02DEF?workspace_id=w>)');
  assert.equal(resolveMediaRefs('![cat](media:01ABC)', () => ''), '*cat*');
  assert.equal(resolveMediaRefs('![](https://example.com/x.png)', url), '![](https://example.com/x.png)');
});

test('reads text from string and part contents', () => {
  assert.equal(messageText('a'), 'a');
  assert.equal(messageText([{ type: 'text', text: 'a' }, { type: 'image', media_id: 'x' }, { type: 'text', text: 'b' }]), 'a\nb');
});
