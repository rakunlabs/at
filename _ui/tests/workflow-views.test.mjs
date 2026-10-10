import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';

const read = (path) => readFile(new URL(path, import.meta.url), 'utf8');
const [routes, sidebar, features, index, runs] = await Promise.all([
  read('../src/routes.ts'), read('../src/lib/components/Sidebar.svelte'),
  read('../src/lib/helper/feature-routes.ts'), read('../src/pages/WorkflowIndex.svelte'),
  read('../src/pages/Runs.svelte'),
]);

test('Runs belongs to Workflows and old bookmarks redirect to the new view', () => {
  assert.match(routes, /'\/runs': redirect\('\/workflows\/runs'\)/);
  assert.match(routes, /'\/workflows\/runs': guarded\(Workflows, '\/workflows\/runs'\)/);
  assert.ok(routes.indexOf("'/workflows/runs':") < routes.indexOf("'/workflows/:id':"));
  assert.doesNotMatch(sidebar, /path:'\/runs'/);
  assert.match(index, /aria-label="Workflow views"/);
  assert.match(index, /href="#\/workflows\/runs"/);
  assert.match(runs, /href=\{`#\/workflows\/\$\{run.workflow_id\}`\}/);
});

test('Workflow builder and Runs retain independent features and lazy page loading', () => {
  assert.match(features, /'\/workflows': FEATURE_WORKFLOW_BUILDER/);
  assert.match(features, /'\/workflows\/runs': FEATURE_WORKFLOW_RUNS/);
  assert.match(index, /routeFeatureEnabled\('\/workflows\/runs'\)/);
  assert.match(index, /runsSelected \? loadRuns\(\) : loadWorkflows\(\)/);
  assert.match(sidebar, /path === '\/workflows' && !routeFeatureEnabled\(path\) \? '\/workflows\/runs' : path/);
});
