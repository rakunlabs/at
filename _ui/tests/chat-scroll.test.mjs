import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

// Extract the two scroll functions through the TypeScript AST and transpile
// them, so type annotations and unrelated runes next to them do not matter.
const source = await readFile(new URL('../src/pages/Chat.svelte', import.meta.url), 'utf8');
const script = source.slice(source.indexOf('>') + 1, source.indexOf('</script>'));
const ast = ts.createSourceFile('Chat.ts', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
const functions = ast.statements
  .filter(node => ts.isFunctionDeclaration(node) && ['handleChatScroll', 'scrollToBottom'].includes(node.name?.text))
  .map(node => node.getText(ast));
const scrollCode = ts.transpileModule(`let followLatest = true;\nlet lastScrollTop = 0;\n${functions.join('\n')}`, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None },
}).outputText;

function harness() {
  const frames = [];
  const container = { scrollHeight: 1000, scrollTop: 600, clientHeight: 400 };
  const controls = new Function('chatContainer', 'requestAnimationFrame', `${scrollCode}; return { handleChatScroll, scrollToBottom };`)(container, fn => frames.push(fn));
  return { container, ...controls, flush() { frames.splice(0).forEach(fn => fn()); } };
}

test('streaming follows the bottom but leaves a reader of older messages in place', () => {
  const h = harness();
  h.handleChatScroll();
  h.container.scrollHeight = 1200;
  h.scrollToBottom();
  h.flush();
  assert.equal(h.container.scrollTop, 1200);

  h.container.scrollTop = 300;
  h.handleChatScroll();
  h.container.scrollHeight = 1400;
  h.scrollToBottom();
  h.flush();
  assert.equal(h.container.scrollTop, 300);

  h.container.scrollTop = 1000;
  h.handleChatScroll();
  h.container.scrollHeight = 1600;
  h.scrollToBottom();
  h.flush();
  assert.equal(h.container.scrollTop, 1600);
});

test('scrolling up cancels a queued follow; opening a conversation resets it', () => {
  const h = harness();
  // The page has already observed the reader at the bottom (every
  // programmatic scroll raises a scroll event); only a move up stops following.
  h.handleChatScroll();
  h.scrollToBottom();
  h.container.scrollTop = 100;
  h.handleChatScroll();
  h.flush();
  assert.equal(h.container.scrollTop, 100);
  h.scrollToBottom(true);
  h.flush();
  assert.equal(h.container.scrollTop, 1000);
});
