import { push } from 'svelte-spa-router';
import { wrap } from 'svelte-spa-router/wrap';
import RouteLoading from '@/lib/components/RouteLoading.svelte';
import RouteLoadError from '@/lib/components/RouteLoadError.svelte';
import { createRouteLoader } from '@/lib/helper/route-loader';
import { isNativeAdmin } from '@/lib/store/auth.svelte';
import { isFeatureEnabled, loadFeatures } from '@/lib/store/features.svelte';
import { routeFeature } from '@/lib/helper/feature-routes';

const Home = () => import('@/pages/Home.svelte');
const Providers = () => import('@/pages/Providers.svelte');
const RoutingProfiles = () => import('@/pages/RoutingProfiles.svelte');
const VirtualProviders = () => import('@/pages/VirtualProviders.svelte');
const Skills = () => import('@/pages/Skills.svelte');
const Marketplaces = () => import('@/pages/Marketplaces.svelte');
const Agents = () => import('@/pages/Agents.svelte');
const Secrets = () => import('@/pages/Secrets.svelte');
const Chat = () => import('@/pages/Chat.svelte');
const SharedChat = () => import('@/pages/SharedChat.svelte');
const ChatSessions = () => import('@/pages/ChatSessions.svelte');
const Tokens = () => import('@/pages/Tokens.svelte');
const NodeConfigs = () => import('@/pages/NodeConfigs.svelte');
const Workflows = () => import('@/pages/Workflows.svelte');
const WorkflowEditor = () => import('@/pages/WorkflowEditor.svelte');
const Runs = () => import('@/pages/Runs.svelte');
const McpServers = () => import('@/pages/McpServers.svelte');
const Mcps = () => import('@/pages/Mcps.svelte');
const Bots = () => import('@/pages/Bots.svelte');
const Docs = () => import('@/pages/Docs.svelte');
const Settings = () => import('@/pages/Settings.svelte');
const SystemSettings = () => import('@/pages/SystemSettings.svelte');
const AccountSecurity = () => import('@/pages/AccountSecurity.svelte');
const AuthenticationSettings = () => import('@/pages/AuthenticationSettings.svelte');
const WorkspaceSettings = () => import('@/pages/WorkspaceSettings.svelte');
const Permissions = () => import('@/pages/Permissions.svelte');
const ExecutionSettings = () => import('@/pages/ExecutionSettings.svelte');
const StorageSettings = () => import('@/pages/StorageSettings.svelte');
const TraceExportSettings = () => import('@/pages/TraceExportSettings.svelte');
const TracePrivacySettings = () => import('@/pages/TracePrivacySettings.svelte');
const GitCredentials = () => import('@/pages/GitCredentials.svelte');
const Organizations = () => import('@/pages/Organizations.svelte');
const OrganizationDetail = () => import('@/pages/OrganizationDetail.svelte');
const Tasks = () => import('@/pages/Tasks.svelte');
const TaskDetail = () => import('@/pages/TaskDetail.svelte');
const Studio = () => import('@/pages/Studio.svelte');
const Webhooks = () => import('@/pages/Webhooks.svelte');
const Crons = () => import('@/pages/Crons.svelte');
const Traces = () => import('@/pages/Traces.svelte');
const Usage = () => import('@/pages/Usage.svelte');
const Pricing = () => import('@/pages/Pricing.svelte');
const Connections = () => import('@/pages/Connections.svelte');
const IntegrationPacks = () => import('@/pages/IntegrationPacks.svelte');
const Files = () => import('@/pages/Files.svelte');
const DeveloperSpaces = () => import('@/pages/DeveloperSpaces.svelte');
const Terminal = () => import('@/pages/Terminal.svelte');
const Features = () => import('@/pages/Features.svelte');
const NotFound = () => import('@/pages/NotFound.svelte');
const Users = () => import('@/pages/Users.svelte');

type PageLoader = () => Promise<{ default: any }>;

function lazy(loader: PageLoader, conditions: Array<() => boolean | Promise<boolean>> = []) {
  return wrap({
    asyncComponent: createRouteLoader(loader, () => ({ default: RouteLoadError })),
    loadingComponent: RouteLoading as any,
    conditions,
  });
}

/**
 * Guards a route on whatever feature owns it in the shared route map, so a
 * catalog change never has to be mirrored here and in the sidebar separately.
 */
function guarded(loader: PageLoader, route: string, ...extra: Array<() => boolean>) {
  const feature = routeFeature(route);
  return lazy(loader, [...extra, async () => {
      try {
        await loadFeatures();
      } catch {
        return true;
      }
      if (!feature || isFeatureEnabled(feature)) return true;
      push('/');
      return false;
    }]);
}

function adminOnly() {
  if (isNativeAdmin()) return true;
  push('/');
  return false;
}

function redirect(to: string) {
  return lazy(Home, [() => {
      push(to);
      return false;
    }]);
}

export default {
  '/': lazy(Home),
  '/terminal': guarded(Terminal, '/terminal', adminOnly),
  '/users': lazy(Users, [adminOnly]),
  '/providers': guarded(Providers, '/providers'),
  '/routing-profiles': guarded(RoutingProfiles, '/routing-profiles'),
  '/virtual-providers': guarded(VirtualProviders, '/virtual-providers'),
  '/skills': guarded(Skills, '/skills'),
  '/marketplaces': guarded(Marketplaces, '/marketplaces'),
  '/agents': guarded(Agents, '/agents'),
  '/variables': guarded(Secrets, '/variables'),
  // One entry, optional param. Two separate entries would be two distinct
  // wrapped objects and would straddle the router's `{#if componentParams}`
  // boundary, so navigating `/chats` → `/chats/:id` would unmount and
  // remount the page — killing the in-flight turn the lazy save depends on.
  '/chats/shared/:id': guarded(SharedChat, '/chats'),
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
  '/settings': lazy(Settings),
  '/settings/system': lazy(SystemSettings),
  '/settings/account': lazy(AccountSecurity),
  '/settings/authentication': lazy(AuthenticationSettings),
  '/settings/workspace': lazy(WorkspaceSettings),
  '/settings/permissions': lazy(Permissions),
  '/settings/execution': guarded(ExecutionSettings, '/settings/execution', adminOnly),
  '/settings/storage': lazy(StorageSettings),
  '/settings/trace-export': lazy(TraceExportSettings),
  '/settings/trace-privacy': lazy(TracePrivacySettings),
  '/settings/git-credentials': lazy(GitCredentials),
  '/settings/users': lazy(Users),
  '/settings/features': lazy(Features),
  '/settings/tokens': guarded(Tokens, '/settings/tokens'),
  '/features': redirect('/settings/features'),
  '/organizations': guarded(Organizations, '/organizations'),
  '/organizations/:id': guarded(OrganizationDetail, '/organizations'),
  '/tasks': guarded(Tasks, '/tasks'),
  '/tasks/:id': guarded(TaskDetail, '/tasks'),
  '/studio': guarded(Studio, '/studio'),
  // One page owns the list, trace detail and session replay so filters and
  // panel state survive navigation between them; /llm-calls is the old URL.
  '/traces': guarded(Traces, '/traces'),
  '/traces/sessions/:session': guarded(Traces, '/traces'),
  '/traces/:id': guarded(Traces, '/traces'),
  '/llm-calls': redirect('/traces'),
  '/usage': guarded(Usage, '/usage'),
  '/pricing': guarded(Pricing, '/pricing'),
  '/files': guarded(Files, '/files'),
  '/developer-spaces': guarded(DeveloperSpaces, '/developer-spaces'),
  '*': lazy(NotFound)
};
