import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';
const source = await readFile(new URL('../src/lib/helper/recovery.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}}).outputText;
const {takeRecoveryTicket} = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
test('recovery fragment is removed before returning a token, including invalid duplicates', () => {
  for (const fragment of ['#recovery=secret%2Fticket','#recovery=one&recovery=two','#recovery=']) {
    let scrubbed = false;
    const result = takeRecoveryTicket({hash:fragment,pathname:'/at/',search:''}, {replaceState(state,title,url) {assert.equal(url,'/at/'); assert.equal(state,null); scrubbed=true;}});
    assert.equal(scrubbed,true); assert.equal(result,fragment === '#recovery=secret%2Fticket' ? 'secret/ticket' : '');
  }
});
test('ordinary SPA fragments are left intact', () => {
  assert.equal(takeRecoveryTicket({hash:'#/settings/account',pathname:'/at/',search:''}, {replaceState(){assert.fail('ordinary route scrubbed');}}),'');
});
