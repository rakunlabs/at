import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';
const source = await readFile(new URL('../src/lib/helper/workspace-selection.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { chooseWorkspace, workspaceSelectionKey } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
const items = [{id:'01new',name:'New',archived:false,created_at:'2026-09-13'}, {id:'legacy-default',name:'Default',archived:false,created_at:'2026-01-01'}, {id:'02old',name:'Old',archived:false,created_at:'2026-02-01'}];
const defaults = {mode:'default',workspace_id:'',last_workspace_id:''};
test('default opens Default rather than whichever ULID sorts first',()=>assert.equal(chooseWorkspace(items,'',defaults).id,'legacy-default'));
test('same-session explicit selection wins over startup preference',()=>assert.equal(chooseWorkspace(items,'01new',defaults).id,'01new'));
test('account preference supports last-used and a pinned workspace',()=>{
  assert.equal(chooseWorkspace(items,'',{...defaults,mode:'last_used',last_workspace_id:'02old'}).id,'02old');
  assert.equal(chooseWorkspace(items,'',{...defaults,mode:'workspace',workspace_id:'01new'}).id,'01new');
});
test('deleted, archived and revoked targets fall back only to accessible workspaces',()=>{
  assert.equal(chooseWorkspace(items,'gone',{...defaults,mode:'workspace',workspace_id:'revoked'}).id,'legacy-default');
  assert.equal(chooseWorkspace(items.map(w=>({...w,archived:w.id==='legacy-default'})),'',defaults).id,'02old');
  assert.equal(chooseWorkspace([],'gone',defaults),undefined);
});
test('selection keys isolate users, new logins and installations',()=>{
  const values=[workspaceSelectionKey('/at/','admin','s1'),workspaceSelectionKey('/at/','reader','s1'),workspaceSelectionKey('/at/','admin','s2'),workspaceSelectionKey('/other/','admin','s1')];
  assert.equal(new Set(values).size,4);
});
