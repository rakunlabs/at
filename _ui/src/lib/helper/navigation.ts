import { can, workspaceState } from '../store/workspace.svelte';
import { isNativeAdmin } from '../store/auth.svelte';
import { isFeatureEnabled } from '../store/features.svelte';
import { FEATURE_WORKSPACE_MANAGEMENT } from '../api/features';
import { routeFeatureEnabled } from './feature-routes';
// Finite presentation registry. Backend remains authoritative for resource selectors.
//
// A route belongs here only when its API is capability-admitted, which means it
// has an entry in `workspaceBusinessPolicies()`
// (internal/server/workspace-business-routes.go). Everything else registered on
// `apiGroup` falls through to `requireWorkspacePlatform(true)` and is
// installation-administrator only, so naming a workspace capability for it
// advertised a page that answered 403 on load — the link looked available and
// the capability it claimed had no bearing on the decision. `/studio` and
// `/files` stay because their data plane is `/api/v1/files/*`, which
// `registerRuntimeRoutes` admits on capabilities; only Studio's one-click setup
// reaches administration APIs.
const capabilityRoutes: Record<string, string> = {
  '/providers': 'providers.read', '/routing-profiles': 'providers.read',
  '/agents': 'agents.read', '/sessions': 'agents.read',
  '/workflows': 'workflows.read', '/runs': 'workflows.read',
  '/bots': 'bots.read', '/organizations': 'organizations.read', '/tasks': 'tasks.read',
  '/studio': 'files.read', '/files': 'files.read',
  '/settings/tokens': 'tokens.read', '/settings/permissions': 'permissions.read',
  '/chats': 'models.use',
  '/usage': 'usage.read', '/llm-calls': 'traces.read',
  '/settings/trace-export': 'workspace.write',
  '/mcps': 'mcp.read', '/skills': 'skills.read',
};
// Installation-administration surfaces. The second row is the set whose APIs are
// registered on `apiGroup` without a business policy: marketplaces, integration
// packs and pack sources, variables,
// node configurations, triggers, connections/connectors/oauth, and MCP servers
// and sets. Scoping any of them backend-side is what moves the route back into
// `capabilityRoutes`; `TestUIPlatformOnlySurfaces` fails when one is. The
// Playground, Usage, Traces and Sessions moved out: they ride models.use,
// usage.read, traces.read and agents.read (chat sessions are owner-scoped in
// the handlers), which the role ladder hands out at member/admin rank.
const platformRoutes = [
  // Policy reads are workspace-admitted, but this configuration surface is
  // only useful to installation administrators who can change the policy.
  '/settings/execution',
  '/terminal', '/users', '/pricing', '/settings/users', '/settings/authentication', '/settings/features', '/settings/media', '/settings/system', '/settings/git-credentials',
  '/marketplaces', '/integrations', '/variables',
  '/node-configs', '/webhooks', '/crons', '/connections', '/mcp-servers',
];
// An account with no membership anywhere resolves nothing: every workspace
// route answers 403, including Documentation, whose guide API is workspace
// scoped. Only the account and workspace pages do real work, plus the Settings
// index that reaches them and Home, which is where the shell explains why the
// rest is missing. Listing anything else produced links that looked available
// and then landed on the same waiting screen.
const unadmittedRoutes = ['/', '/settings', '/settings/account', '/settings/workspace'];
export function workspaceAdmitted() {
  return isNativeAdmin() || !!workspaceState.access;
}
export function routeAllowed(route: string) {
  if (!workspaceAdmitted()) return unadmittedRoutes.includes(route);
  if (platformRoutes.some(p => route === p || route.startsWith(p + '/'))) return isNativeAdmin();
  if (['/', '/docs', '/settings', '/settings/account', '/settings/workspace', '/settings/permissions'].includes(route)) return true;
  const key = Object.keys(capabilityRoutes).sort((a,b) => b.length-a.length).find(p => route === p || route.startsWith(p + '/'));
  return key ? can(capabilityRoutes[key]) : isNativeAdmin();
}
// Settings indexes listed pages whose router guard bounces straight back to
// Home when their feature is off, so they filter on both.
export function settingsLinkVisible(route: string) {
  // Single-workspace mode pins every request to Default, so the Workspace page
  // keeps only a sign-in preference that cannot vary and sections whose APIs
  // answer 404. It is not offered; the route stays reachable for a direct link,
  // because it is also the join-a-workspace escape hatch once re-enabled.
  if (route === '/settings/workspace' && !isFeatureEnabled(FEATURE_WORKSPACE_MANAGEMENT)) return false;
  return routeAllowed(route) && routeFeatureEnabled(route);
}
export const configurationLinks = [
  { path: '/settings/account', label: 'Account security', description: 'Password, passkeys, linked accounts and authenticator' },
  { path: '/settings/workspace', label: 'Workspace', description: 'Members, invitations and workspace details' },
  { path: '/settings/permissions', label: 'Permissions', description: 'Bundles, denies, mappings and effective access' },
  { path: '/settings/authentication', label: 'Authentication', description: 'Local sign-in, admission policy and identity providers' },
  { path: '/settings/users', label: 'Users', description: 'Installation accounts and full account recovery' },
  { path: '/settings/execution', label: 'Execution', description: 'Restricted and trusted-host policy' },
  { path: '/settings/media', label: 'Media storage', description: 'Where image attachments in Chats are stored' },
  { path: '/settings/trace-export', label: 'Trace export', description: 'Workspace trace delivery to OpenTelemetry or Langfuse' },
  { path: '/settings/git-credentials', label: 'Git credentials', description: 'Deploy keys for private skill repositories' },
  { path: '/routing-profiles', label: 'Routing profiles', description: 'Named model chains with automatic fallback' },
  { path: '/connections', label: 'Connections', description: 'External-service credentials and connectors' },
  { path: '/variables', label: 'Variables', description: 'Workspace variables and secrets' },
  { path: '/mcp-servers', label: 'MCP servers', description: 'Tool servers and endpoints' },
  { path: '/node-configs', label: 'Node configurations', description: 'Reusable workflow node settings' },
  { path: '/webhooks', label: 'Webhooks', description: 'Workflow HTTP triggers' },
  { path: '/crons', label: 'Schedules', description: 'Scheduled workflow triggers' },
  { path: '/marketplaces', label: 'Marketplaces', description: 'Skill catalogs and installation' },
  { path: '/settings/tokens', label: 'API tokens', description: 'Workspace gateway credentials' },
  { path: '/settings/features', label: 'Features', description: 'Installation feature availability' },
  { path: '/pricing', label: 'Pricing', description: 'Model pricing configuration' },
  { path: '/settings/system', label: 'System', description: 'Encryption and build information' },
];
// The settings layout covers `/settings/*` plus the configuration pages it
// links to, which live at top-level routes. The shell uses this to swap in the
// settings sidebar and the main sidebar to mark Settings as the current
// section; they kept separate copies of the expression, so a link added here
// changed the layout without changing what looked selected.
export function inSettingsArea(route: string) {
  return route.startsWith('/settings') || configurationLinks.some(l => l.path === route);
}
