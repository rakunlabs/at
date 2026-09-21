import { push } from 'svelte-spa-router';
import { wrap } from 'svelte-spa-router/wrap';
import Home from '@/pages/Home.svelte';
import Providers from '@/pages/Providers.svelte';
import RoutingProfiles from '@/pages/RoutingProfiles.svelte';
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
import McpServers from '@/pages/McpServers.svelte';
import Mcps from '@/pages/Mcps.svelte';
import Bots from '@/pages/Bots.svelte';
import Docs from '@/pages/Docs.svelte';
import Settings from '@/pages/Settings.svelte';
import SystemSettings from '@/pages/SystemSettings.svelte';
import AccountSecurity from '@/pages/AccountSecurity.svelte';
import AuthenticationSettings from '@/pages/AuthenticationSettings.svelte';
import WorkspaceSettings from '@/pages/WorkspaceSettings.svelte';
import Permissions from '@/pages/Permissions.svelte';
import ExecutionSettings from '@/pages/ExecutionSettings.svelte';
import MediaSettings from '@/pages/MediaSettings.svelte';
import TraceExportSettings from '@/pages/TraceExportSettings.svelte';
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
import Terminal from '@/pages/Terminal.svelte';
import Features from '@/pages/Features.svelte';
import NotFound from '@/pages/NotFound.svelte';
import Users from '@/pages/Users.svelte';
import { isNativeAdmin } from '@/lib/store/auth.svelte';
import { isFeatureEnabled, loadFeatures } from '@/lib/store/features.svelte';
import { routeFeature } from '@/lib/helper/feature-routes';

/**
 * Guards a route on whatever feature owns it in the shared route map, so a
 * catalog change never has to be mirrored here and in the sidebar separately.
 */
function guarded(component: any, route: string, ...extra: Array<() => boolean>) {
  const feature = routeFeature(route);
  return wrap({
    component,
    conditions: [...extra, async () => {
      try {
        await loadFeatures();
      } catch {
        return true;
      }
      if (!feature || isFeatureEnabled(feature)) return true;
      push('/');
      return false;
    }],
  });
}

function adminOnly() {
  if (isNativeAdmin()) return true;
  push('/');
  return false;
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
  '/terminal': guarded(Terminal as any, '/terminal', adminOnly),
  '/users': wrap({ component: Users as any, conditions: [adminOnly] }),
  '/providers': guarded(Providers, '/providers'),
  '/routing-profiles': guarded(RoutingProfiles, '/routing-profiles'),
  '/skills': guarded(Skills, '/skills'),
  '/marketplaces': guarded(Marketplaces, '/marketplaces'),
  '/agents': guarded(Agents, '/agents'),
  '/variables': guarded(Secrets, '/variables'),
  // One entry, optional param. Two separate entries would be two distinct
  // wrapped objects and would straddle the router's `{#if componentParams}`
  // boundary, so navigating `/chats` → `/chats/:id` would unmount and
  // remount the page — killing the in-flight turn the lazy save depends on.
  '/chats/:id?': guarded(Chat, '/chats'),
  '/sessions': guarded(ChatSessions, '/sessions'),
  '/tokens': redirect('/settings/tokens'),
  '/node-configs': guarded(NodeConfigs, '/node-configs'),
  '/workflows': guarded(Workflows, '/workflows'),
  '/workflows/:id': guarded(WorkflowEditor, '/workflows'),
  '/runs': guarded(Runs, '/runs'),
  '/webhooks': guarded(Webhooks, '/webhooks'),
  '/crons': guarded(Crons, '/crons'),
  '/connections': guarded(Connections, '/connections'),
  '/integrations': guarded(IntegrationPacks, '/integrations'),
  '/mcp-servers': guarded(McpServers, '/mcp-servers'),
  '/mcps': guarded(Mcps, '/mcps'),
  '/bots': guarded(Bots, '/bots'),
  '/docs': guarded(Docs, '/docs'),
  '/settings': Settings,
  '/settings/system': SystemSettings,
  '/settings/account': AccountSecurity,
  '/settings/authentication': AuthenticationSettings,
  '/settings/workspace': WorkspaceSettings,
  '/settings/permissions': Permissions,
  '/settings/execution': guarded(ExecutionSettings, '/settings/execution', adminOnly),
  '/settings/media': MediaSettings,
  '/settings/trace-export': TraceExportSettings,
  '/settings/users': Users,
  '/settings/features': Features,
  '/settings/tokens': guarded(Tokens, '/settings/tokens'),
  '/features': redirect('/settings/features'),
  '/organizations': guarded(Organizations, '/organizations'),
  '/organizations/:id': guarded(OrganizationDetail, '/organizations'),
  '/tasks': guarded(Tasks, '/tasks'),
  '/tasks/:id': guarded(TaskDetail, '/tasks'),
  '/studio': guarded(Studio, '/studio'),
  '/llm-calls': guarded(LLMCalls, '/llm-calls'),
  '/usage': guarded(Usage, '/usage'),
  '/pricing': guarded(Pricing, '/pricing'),
  '/files': guarded(Files, '/files'),
  '*': NotFound
};
