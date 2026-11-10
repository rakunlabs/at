import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

async function load(file, replacements = []) {
  let source = await readFile(new URL(file, import.meta.url), 'utf8');
  for (const [from, to] of replacements) source = source.replace(from, to);
  const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
  return import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
}

const helper = await load('../src/lib/helper/developer-space.ts');
const pageSource = await readFile(new URL('../src/pages/DeveloperSpaces.svelte', import.meta.url), 'utf8');

test('developer sidebars use the shared textured navigation ground, not a flat raised surface', async () => {
  const sidebars = [...pageSource.matchAll(/<aside class="([^"]+)"/g)];
  assert.equal(sidebars.length, 2);
  for (const [, classes] of sidebars) {
    assert.match(classes, /\bgrain-background\b/);
    assert.match(classes, /\bbg-dark-base\b/);
    assert.doesNotMatch(classes, /\bbg-dark-surface\b/);
  }
  const css = await readFile(new URL('../src/style/global.css', import.meta.url), 'utf8');
  assert.match(css, /\.grain-background[^\{]*\{\s*background-image: var\(--grain\);\s*background-size: 2px 2px/);
  assert.match(css, /@media \(prefers-contrast: more\) \{[^}]*\.grain-background[^}]*background-image: none/);
});

test('Kubernetes stop warns about package loss and displays runtime limitations', () => {
  const stop = pageSource.match(/async function stop\(\) \{([\s\S]*?)\n  \}/)[1];
  assert.match(stop, /runtime\?\.backend === 'kubernetes'/);
  assert.match(stop, /installed system packages will be lost/);
  assert.match(pageSource, /space\.runtime\.notice/);
  assert.match(pageSource, /Use a prepared Linux image/);
});

test('Developer Spaces has no AI chat; it links to Chats instead', async () => {
  assert.doesNotMatch(pageSource, /SessionChat|createDeveloperSession|listDeveloperSessions|newSession/);
  assert.match(pageSource, /href=\{chatProjectLink\(project\)\}/);
  await assert.rejects(readFile(new URL('../src/lib/components/developer/SessionChat.svelte', import.meta.url)), /ENOENT/);
});

test('links from Chats select the project and open the file once attached', () => {
  assert.match(pageSource, /query\.get\('project'\)/);
  assert.match(pageSource, /pendingFile = query\.get\('file'\)/);
  const attach = pageSource.match(/async function attach\(\) \{([\s\S]*?)\n  \}/)[1];
  assert.match(attach, /if \(pendingFile\)/);
});

test('space suspension retains history and requires explicit control recovery', () => {
  assert.match(pageSource, /if \(starting \|\| space\?\.active_control_id\) return/);
  assert.match(pageSource, /space\?\.execution_suspended && !starting/);
  assert.match(pageSource, /Files are kept/);
  assert.match(pageSource, /Work is never restarted automatically/);
  for (const name of ['stop', 'reset']) {
    const action = pageSource.match(new RegExp(`async function ${name}\\(\\) \\{([\\s\\S]*?)\\n  \\}`))[1];
    assert.match(action, /space = await getDeveloperSpace\(\)/);
    assert.match(action, /if \(space.execution_suspended\)/);
  }
});

test('opening Developer Spaces never starts the container; it attaches only to one already running', () => {
  const boot = pageSource.match(/async function boot\(\) \{([\s\S]*?)\n  \}/)[1];
  assert.match(boot, /await getDeveloperSpace\(\)/);
  assert.doesNotMatch(boot, /\bstart\(|startDeveloperSpace\(|loadRoot\(|openProject\(|newTerminal\(/);
  assert.match(boot, /space\.status === 'ready' && !space\.execution_suspended && !space\.active_control_id\) await attach\(\)/);
  assert.match(pageSource, /onclick=\{start\}/);
});

test('file and project controls require explicit runtime connection, even with cached listings', () => {
  assert.match(pageSource, /runtimeAvailable = \$derived\(runtimeConnected && space\?\.status === 'ready' && !space.execution_suspended && !space.active_control_id\)/);
  assert.match(pageSource, /disabled=\{!runtimeAvailable\} onclick=\{event => \{ event.stopPropagation\(\); projectMenu/);
  assert.match(pageSource, /sidebarView === 'files' && runtimeAvailable/);
  assert.match(pageSource, /\{#if !runtimeAvailable\}[\s\S]*?Start the space to browse, create or upload files/);
  assert.doesNotMatch(pageSource, /!Object.keys\(listings\).length/);
  assert.match(pageSource, /\{#if menu && runtimeAvailable\}/);
  assert.match(pageSource, /\{#if newProjectMode && runtimeAvailable\}/);
});

test('file actions, shortcuts and late upload callbacks cannot start a disconnected runtime', async () => {
  const names = ['loadFolder', 'refreshTree', 'toggleFolder', 'refreshGitMarks', 'openFile', 'loadFileTab',
    'saveTab', 'overwrite', 'download', 'showMenu', 'createEntry', 'renameEntry',
    'deleteEntry', 'pickUpload', 'uploadFiles', 'createProject', 'openDiff', 'loadDiff', 'runSearch', 'newTerminal'];
  for (const name of names) {
    const source = pageSource.match(new RegExp(`(?:async )?function ${name}\\([^\\n]*\\) \\{[\\s\\S]*?\\n  \\}`))?.[0];
    assert.ok(source, name);
    const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
    // No API, prompt, DOM or other dependencies are supplied: touching any of
    // them before checking runtime availability makes this invocation fail.
    const action = new Function('runtimeAvailable', `${code}; return ${name};`)(false);
    await action();
  }
});

test('switching projects while disconnected only changes metadata', async () => {
  const source = pageSource.match(/async function openProject\([^\n]*\) \{[\s\S]*?\n  \}/)[0];
  const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
  const result = await new Function(`let project = '', projectMenu = true; const runtimeAvailable = false;
    ${code}; return openProject('saved-project').then(() => ({ project, projectMenu }));`)();
  assert.deepEqual(result, { project: 'saved-project', projectMenu: false });
});

test('path helpers stay inside the given folder', () => {
  assert.equal(helper.parentPath('a/b/c.go'), 'a/b');
  assert.equal(helper.parentPath('top'), '');
  assert.equal(helper.joinPath('', 'x'), 'x');
  assert.equal(helper.joinPath('a', 'x'), 'a/x');
  assert.equal(helper.isWithin('a/b', 'a'), true);
  assert.equal(helper.isWithin('ab/c', 'a'), false, 'a sibling with a shared prefix is not inside');
  assert.equal(helper.renamedPath('a/b/c', 'a/b', 'a/z'), 'a/z/c');
  assert.equal(helper.renamedPath('a/bc', 'a/b', 'a/z'), 'a/bc');
});

test('entry names are single segments', () => {
  for (const bad of ['', '  ', '.', '..', 'a/b', 'a\\b']) assert.notEqual(helper.validEntryName(bad), null, JSON.stringify(bad));
  assert.equal(helper.validEntryName('main.go'), null);
});

test('links between Chats and Developer Spaces carry the project and file', () => {
  assert.equal(helper.chatProjectLink('notes'), '#/chats?developer=1&project=notes');
  assert.equal(helper.chatProjectLink(''), '#/chats?developer=1');
  assert.equal(helper.developerFileLink('notes', 'daily/a b.md'), '#/developer-spaces?project=notes&file=notes%2Fdaily%2Fa+b.md');
});
