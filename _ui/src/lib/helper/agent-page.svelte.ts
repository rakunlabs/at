import { listAgents, type Agent } from '../api/agents';
import { listProviders, type ProviderRecord } from '../api/providers';
import { listSkills, type Skill } from '../api/skills';
import { listMCPSets, type MCPSet } from '../api/mcp-sets';
import { listWorkflows, type Workflow } from '../api/workflows';
import { listBuiltinTools, type BuiltinToolDef } from '../api/mcp';
import { listConnections, type Connection } from '../api/connections';
import { loadFeatures, isFeatureEnabled } from '../store/features.svelte';
import { createPageLoader } from './page-load.svelte';

/** The list has no dependency on the catalogs used by the editor. */
export function createAgentPage() {
  const data = $state({
    agents: [] as Agent[], providers: [] as ProviderRecord[], skills: [] as Skill[],
    mcpSets: [] as MCPSet[], workflows: [] as Workflow[],
    builtinToolDefs: [] as BuiltinToolDef[], connections: [] as Connection[],
  });
  const list = createPageLoader();
  const editor = createPageLoader();
  let editorGeneration = 0;

  async function loadList() {
    list.reset();
    // Route admission already handles agents. Do not make rendering successful
    // agent data wait for feature discovery or unrelated editor endpoints.
    return list.load('Agents', () => listAgents({ _offset: 0, _limit: 1000 }), result => {
      data.agents = result.data || [];
    });
  }

  async function loadEditor() {
    const generation = ++editorGeneration;
    editor.reset();
    // Unknown availability is not permission to fan out to optional catalogs.
    // An unavailable catalog is reported inside the editor, never over the list.
    const ready = await editor.load('Feature availability', loadFeatures, () => {});
    if (!ready || generation !== editorGeneration) return;
    // Discard old choices when a feature was disabled between editor openings.
    // The form's saved selections are independent and are preserved on save.
    if (!isFeatureEnabled('skills')) data.skills = [];
    if (!isFeatureEnabled('mcp_servers')) data.mcpSets = [];
    if (!isFeatureEnabled('builtin_tools')) data.builtinToolDefs = [];
    if (!isFeatureEnabled('external_connections')) data.connections = [];
    if (!isFeatureEnabled('workflow_builder')) data.workflows = [];
    await Promise.all([
      editor.load('Providers', listProviders, result => { data.providers = result.data || []; }),
      isFeatureEnabled('skills') && editor.load('Skills', listSkills, result => { data.skills = result.data || []; }),
      isFeatureEnabled('mcp_servers') && editor.load('MCP sets', () => listMCPSets({ _limit: 500 }), result => { data.mcpSets = result.data || []; }),
      isFeatureEnabled('builtin_tools') && editor.load('Built-in tools', listBuiltinTools, result => { data.builtinToolDefs = result.tools || []; }),
      isFeatureEnabled('external_connections') && editor.load('Connections', listConnections, result => { data.connections = result || []; }),
      isFeatureEnabled('workflow_builder') && editor.load('Workflows', () => listWorkflows({ _limit: 500 }), result => { data.workflows = result.data || []; }),
    ]);
  }

  function closeEditor() {
    editorGeneration++;
    editor.reset();
  }

  return {
    data, list, editor, loadList, loadEditor, closeEditor,
    get editorLoading() {
      return ['Feature availability', 'Providers', 'Skills', 'MCP sets', 'Built-in tools', 'Connections', 'Workflows'].some(label => editor.loading(label));
    },
  };
}
