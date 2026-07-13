import { push } from 'svelte-spa-router';
import { wrap } from 'svelte-spa-router/wrap';
import Home from '@/pages/Home.svelte';
import Providers from '@/pages/Providers.svelte';
import Skills from '@/pages/Skills.svelte';
import Marketplaces from '@/pages/Marketplaces.svelte';
import Agents from '@/pages/Agents.svelte';
import Secrets from '@/pages/Secrets.svelte';
import Chat from '@/pages/Chat.svelte';
import ChatSessions from '@/pages/ChatSessions.svelte';
import Tokens from '@/pages/Tokens.svelte';
import NodeConfigs from '@/pages/NodeConfigs.svelte';
import Workflows from '@/pages/Workflows.svelte';
import WorkflowEditor from '@/pages/WorkflowEditor.svelte';
import Runs from '@/pages/Runs.svelte';
import Rag from '@/pages/Rag.svelte';
import McpServers from '@/pages/McpServers.svelte';
import Mcps from '@/pages/Mcps.svelte';
import Bots from '@/pages/Bots.svelte';
import Docs from '@/pages/Docs.svelte';
import Settings from '@/pages/Settings.svelte';
import Organizations from '@/pages/Organizations.svelte';
import OrganizationDetail from '@/pages/OrganizationDetail.svelte';
import Tasks from '@/pages/Tasks.svelte';
import TaskDetail from '@/pages/TaskDetail.svelte';
import Studio from '@/pages/Studio.svelte';
import Webhooks from '@/pages/Webhooks.svelte';
import Crons from '@/pages/Crons.svelte';
import LLMCalls from '@/pages/LLMCalls.svelte';
import Usage from '@/pages/Usage.svelte';
import Pricing from '@/pages/Pricing.svelte';
import Connections from '@/pages/Connections.svelte';
import IntegrationPacks from '@/pages/IntegrationPacks.svelte';
import Files from '@/pages/Files.svelte';
import Features from '@/pages/Features.svelte';
import NotFound from '@/pages/NotFound.svelte';
import { isFeatureEnabled, loadFeatures } from '@/lib/store/features.svelte';
import {
  FEATURE_AGENTS,
  FEATURE_AUTOMATION,
  FEATURE_CHAT_WORKBENCH,
  FEATURE_CONNECTIONS,
  FEATURE_FILES,
  FEATURE_ORGANIZATION_WORKFLOWS,
  FEATURE_PROVIDER_SETUP,
  FEATURE_RAG,
} from '@/lib/api/features';

function guarded(component: any, feature: string) {
  return wrap({
    component,
    conditions: [async () => {
      try {
        await loadFeatures();
      } catch {
        return true;
      }
      if (isFeatureEnabled(feature)) return true;
      push('/');
      return false;
    }],
  });
}

function redirect(to: string) {
  return wrap({
    component: Home as any,
    conditions: [() => {
      push(to);
      return false;
    }],
  });
}

export default {
  '/': Home,
  '/providers': guarded(Providers, FEATURE_PROVIDER_SETUP),
  '/skills': Skills,
  '/marketplaces': Marketplaces,
  '/agents': guarded(Agents, FEATURE_AGENTS),
  '/variables': Secrets,
  '/chat': guarded(Chat, FEATURE_CHAT_WORKBENCH),
  '/sessions': guarded(ChatSessions, FEATURE_CHAT_WORKBENCH),
  '/tokens': redirect('/settings/tokens'),
  '/node-configs': guarded(NodeConfigs, FEATURE_AUTOMATION),
  '/workflows': guarded(Workflows, FEATURE_AUTOMATION),
  '/workflows/:id': guarded(WorkflowEditor, FEATURE_AUTOMATION),
  '/runs': guarded(Runs, FEATURE_AUTOMATION),
  '/webhooks': guarded(Webhooks, FEATURE_AUTOMATION),
  '/crons': guarded(Crons, FEATURE_AUTOMATION),
  '/rag': guarded(Rag, FEATURE_RAG),
  '/connections': guarded(Connections, FEATURE_CONNECTIONS),
  '/integrations': guarded(IntegrationPacks, FEATURE_CONNECTIONS),
  '/mcp-servers': McpServers,
  '/mcps': Mcps,
  '/bots': guarded(Bots, FEATURE_CHAT_WORKBENCH),
  '/docs': Docs,
  '/settings': Settings,
  '/settings/features': Features,
  '/settings/tokens': Tokens,
  '/features': redirect('/settings/features'),
  '/organizations': guarded(Organizations, FEATURE_ORGANIZATION_WORKFLOWS),
  '/organizations/:id': guarded(OrganizationDetail, FEATURE_ORGANIZATION_WORKFLOWS),
  '/tasks': guarded(Tasks, FEATURE_ORGANIZATION_WORKFLOWS),
  '/tasks/:id': guarded(TaskDetail, FEATURE_ORGANIZATION_WORKFLOWS),
  '/studio': guarded(Studio, FEATURE_ORGANIZATION_WORKFLOWS),
  '/llm-calls': LLMCalls,
  '/usage': guarded(Usage, FEATURE_PROVIDER_SETUP),
  '/pricing': guarded(Pricing, FEATURE_PROVIDER_SETUP),
  '/files': guarded(Files, FEATURE_FILES),
  '*': NotFound
};
