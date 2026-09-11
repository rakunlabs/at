import { can } from '../store/workspace.svelte';
import { isNativeAdmin } from '../store/auth.svelte';
// Finite presentation registry. Backend remains authoritative for resource selectors.
const capabilityRoutes: Record<string, string> = {
  '/providers': 'providers.read', '/skills': 'skills.read', '/marketplaces': 'packs.read',
  '/agents': 'agents.read', '/variables': 'variables.read',
  // `/playground/:id` is covered by the longest-prefix match below.
  '/playground': 'models.use', '/sessions': 'agents.read', '/node-configs': 'workflows.read',
  '/workflows': 'workflows.read', '/runs': 'workflows.read', '/webhooks': 'workflows.read',
  '/crons': 'workflows.read', '/connections': 'connections.read', '/integrations': 'packs.read',
  '/mcp-servers': 'mcp.read', '/mcps': 'mcp.read', '/bots': 'bots.read',
  '/organizations': 'organizations.read', '/tasks': 'tasks.read', '/studio': 'files.read',
  '/llm-calls': 'traces.read', '/usage': 'usage.read', '/files': 'files.read',
  '/settings/tokens': 'tokens.read', '/settings/permissions': 'permissions.read', '/settings/execution': 'workspace.read',
};
const platformRoutes = ['/users', '/pricing', '/settings/users', '/settings/authentication', '/settings/features', '/settings/media', '/settings/system'];
export function routeAllowed(route: string) {
  if (platformRoutes.some(p => route === p || route.startsWith(p + '/'))) return isNativeAdmin();
  if (['/', '/docs', '/settings', '/settings/account', '/settings/workspace', '/settings/permissions'].includes(route)) return true;
  const key = Object.keys(capabilityRoutes).sort((a,b) => b.length-a.length).find(p => route === p || route.startsWith(p + '/'));
  return key ? can(capabilityRoutes[key]) : isNativeAdmin();
}
export const configurationLinks = [
  { path: '/settings/account', label: 'Account security', description: 'Password, passkeys, linked accounts and authenticator' },
  { path: '/settings/workspace', label: 'Workspace', description: 'Members, invitations and workspace details' },
  { path: '/settings/permissions', label: 'Permissions', description: 'Bundles, denies, mappings and effective access' },
  { path: '/settings/authentication', label: 'Authentication', description: 'Local sign-in, admission policy and identity providers' },
  { path: '/settings/users', label: 'Users', description: 'Installation accounts and full account recovery' },
  { path: '/settings/execution', label: 'Execution', description: 'Restricted and trusted-host policy' },
  { path: '/settings/media', label: 'Media storage', description: 'Where Playground image attachments are stored' },
  { path: '/providers', label: 'Model providers', description: 'Model connections and credentials' },
  { path: '/connections', label: 'Connections', description: 'External-service credentials and connectors' },
  { path: '/variables', label: 'Variables', description: 'Workspace variables and secrets' },
  { path: '/mcp-servers', label: 'MCP servers', description: 'Tool servers and endpoints' },
  { path: '/mcps', label: 'MCP sets', description: 'Reusable tool collections' },
  { path: '/node-configs', label: 'Node configurations', description: 'Reusable workflow node settings' },
  { path: '/webhooks', label: 'Webhooks', description: 'Workflow HTTP triggers' },
  { path: '/crons', label: 'Schedules', description: 'Scheduled workflow triggers' },
  { path: '/marketplaces', label: 'Marketplaces', description: 'Skill catalogs and installation' },
  { path: '/settings/tokens', label: 'API tokens', description: 'Workspace gateway credentials' },
  { path: '/settings/features', label: 'Features', description: 'Installation feature availability' },
  { path: '/pricing', label: 'Pricing', description: 'Model pricing configuration' },
  { path: '/settings/system', label: 'System', description: 'Encryption and build information' },
];
