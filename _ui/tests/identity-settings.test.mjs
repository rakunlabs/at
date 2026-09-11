import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';
async function moduleURL(path, replacements = []) {
  const source = await readFile(new URL(path, import.meta.url), 'utf8');
  let code = ts.transpileModule(source, {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}}).outputText;
  for (const [from,to] of replacements) code = code.replace(from,to);
  return `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
}
const identityURL = await moduleURL('../src/lib/api/identity.ts', [["from 'axios'", `from '${import.meta.resolve('axios')}'`]]);
const {validateSettings} = await import(identityURL);
const {validAuthMessage,localContinuation} = await import(await moduleURL('../src/lib/helper/auth-popup.ts', [["from '../api/identity'", `from '${identityURL}'`]]));
test('auth settings enforce version, integral bounded lifetimes and immutable security policy fields', () => {
  const base = {version:1,origin:'https://at.example',session_ttl_seconds:28800,remember_ttl_seconds:2592000,signup_admission:'invite_only',mfa_policy:'enrolled_required',max_sessions:20};
  assert.equal(validateSettings(base),'');
  for (const patch of [{version:0},{version:1.5},{session_ttl_seconds:599},{session_ttl_seconds:86401},{session_ttl_seconds:600.5},{remember_ttl_seconds:1000},{remember_ttl_seconds:2592001},{signup_admission:'open'},{mfa_policy:'optional'},{max_sessions:100}]) assert.ok(validateSettings({...base,...patch}),JSON.stringify(patch));
});
test('external bridge requires both exact origin and opened popup identity', () => {
  const popup = {}; const data = {type:'at-auth-result',result:{mfa_required:true,challenge:'opaque'}};
  assert.equal(validAuthMessage({origin:'https://at.example',source:popup,data},popup,'https://at.example'),true);
  for (const event of [{origin:'https://evil.example',source:popup,data},{origin:'https://at.example',source:{},data},{origin:'https://at.example',source:popup,data:{type:'other',result:{}}},{origin:'https://at.example',source:popup,data:{type:'at-auth-result',result:null}}]) assert.equal(validAuthMessage(event,popup,'https://at.example'),false);
});
test('continuations are local paths and reject scheme-relative, foreign and backslash targets', () => {
  const base = 'https://at.example/at/';
  assert.equal(localContinuation('/at/#/mobile-authorize?request_id=id',base),'/at/#/mobile-authorize?request_id=id');
  for (const value of ['//evil.example','https://evil.example','/\\evil.example','javascript:alert(1)','/path\nheader',null]) assert.equal(localContinuation(value,base),undefined);
});
