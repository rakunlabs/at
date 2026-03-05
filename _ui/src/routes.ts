import Home from '@/pages/Home.svelte';
import Providers from '@/pages/Providers.svelte';
import Skills from '@/pages/Skills.svelte';
import Agents from '@/pages/Agents.svelte';
import Secrets from '@/pages/Secrets.svelte';
import Chat from '@/pages/Chat.svelte';
import Tokens from '@/pages/Tokens.svelte';
import NodeConfigs from '@/pages/NodeConfigs.svelte';
import Workflows from '@/pages/Workflows.svelte';
import WorkflowEditor from '@/pages/WorkflowEditor.svelte';
import Runs from '@/pages/Runs.svelte';
import Rag from '@/pages/Rag.svelte';
import McpServers from '@/pages/McpServers.svelte';
import Docs from '@/pages/Docs.svelte';
import Settings from '@/pages/Settings.svelte';
import NotFound from '@/pages/NotFound.svelte';

export default {
  '/': Home,
  '/providers': Providers,
  '/skills': Skills,
  '/agents': Agents,
  '/variables': Secrets,
  '/chat': Chat,
  '/tokens': Tokens,
  '/node-configs': NodeConfigs,
  '/workflows': Workflows,
  '/workflows/:id': WorkflowEditor,
  '/runs': Runs,
  '/rag': Rag,
  '/mcp-servers': McpServers,
  '/docs': Docs,
  '/settings': Settings,
  '*': NotFound
};
