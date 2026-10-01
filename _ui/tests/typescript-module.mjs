import { readFile } from 'node:fs/promises';
import ts from 'typescript';

const cache = new Map();

/** Load small helper modules, resolving their relative TS dependencies in memory. */
export async function moduleURL(url) {
  if (cache.has(url.href)) return cache.get(url.href);
  let code = ts.transpileModule(await readFile(url, 'utf8'), {
    compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
  }).outputText;
  for (const match of [...code.matchAll(/from (['"])(\.\.?\/[^'"]+)\1/g)]) {
    const path = match[2];
    const dependency = await moduleURL(new URL(path.endsWith('.ts') ? path : `${path}.ts`, url));
    code = code.replaceAll(`${match[1]}${path}${match[1]}`, `'${dependency}'`);
  }
  const result = `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
  cache.set(url.href, result);
  return result;
}
