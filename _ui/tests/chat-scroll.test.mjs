import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const source = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');
const css = await readFile(new URL('../src/style/global.css', import.meta.url), 'utf8');
const body = source.match(/function updateScrollPosition\(\) \{([\s\S]*?)\n  \}/)[1];
const position = new Function('chatContainer', `let awayFromLatest, scrollThumbHeight, scrollThumbTop; ${body}; return { awayFromLatest, scrollThumbHeight, scrollThumbTop };`);

test('latest button follows actual scroll distance and tolerates the end', () => {
  assert.equal(position({ scrollTop: 0, scrollHeight: 2000, clientHeight: 500 }).awayFromLatest, true);
  assert.equal(position({ scrollTop: 1480, scrollHeight: 2000, clientHeight: 500 }).awayFromLatest, false);
  assert.equal(position({ scrollTop: 0, scrollHeight: 400, clientHeight: 500 }).awayFromLatest, false);
  assert.match(source, /onclick=\{\(\) => scrollToBottom\(true\)\}/);
  assert.match(source, /Jump to latest <ArrowDown/);
});

test('overlay thumb stays within viewport and native scrollbar consumes no width', () => {
  assert.deepEqual(position({ scrollTop: 750, scrollHeight: 2000, clientHeight: 500 }), { awayFromLatest: true, scrollThumbHeight: 125, scrollThumbTop: 187.5 });
  assert.equal(position({ scrollTop: 5000, scrollHeight: 2000, clientHeight: 500 }).scrollThumbTop, 375);
  assert.equal(position({ scrollTop: 0, scrollHeight: 400, clientHeight: 500 }).scrollThumbHeight, 0);
  assert.match(css, /\.chat-transcript-scroller\s*\{\s*scrollbar-width: none;/);
  assert.match(css, /\.chat-transcript-scroller::-webkit-scrollbar\s*\{\s*display: none;/);
  assert.match(source, /scrollIndicatorVisible && scrollThumbHeight > 0/);
  assert.match(source, /scrollIndicatorVisible = false; \}, 700\)/);
  assert.match(source, /if \(scrollIndicatorTimer\) clearTimeout\(scrollIndicatorTimer\)/);
});
