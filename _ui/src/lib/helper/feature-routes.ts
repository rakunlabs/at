import {
  FEATURE_AGENTS,
  FEATURE_API_TOKENS,
  FEATURE_ROUTING_PROFILES,
  FEATURE_BOTS,
  FEATURE_CHAT_SESSIONS,
  FEATURE_EXTERNAL_CONNECTIONS,
  FEATURE_FILES,
  FEATURE_GOALS_PROJECTS,
  FEATURE_GUIDES,
  FEATURE_INTEGRATION_PACKS,
  FEATURE_LLM_TRACES,
  FEATURE_MARKETPLACES,
  FEATURE_MCP_SERVERS,
  FEATURE_MODEL_PRICING,
  FEATURE_ORGANIZATIONS,
  FEATURE_PLAYGROUND,
  FEATURE_PROVIDER_SETUP,
  FEATURE_SKILLS,
  FEATURE_STUDIO,
  FEATURE_TASKS,
  FEATURE_TERMINAL,
  FEATURE_USAGE_ANALYTICS,
  FEATURE_VARIABLES,
  FEATURE_WORKFLOW_BUILDER,
  FEATURE_WORKFLOW_RUNS,
} from '@/lib/api/features';
import { isFeatureEnabled } from '@/lib/store/features.svelte';

/**
 * The single route → feature map. The sidebar, the router guards and the
 * settings index all read it: they used to keep separate copies, which drifted
 * (the Traces link stayed visible while its API answered 404, and Settings kept
 * offering pages whose route bounced straight back to Home).
 *
 * Keys are route prefixes; the longest match wins, so `/workflows/:id` inherits
 * `/workflows`. Routes absent from this map are never feature-gated — that is
 * deliberate for `/`, `/users` and everything under `/settings` except the
 * entries listed here, because those are how an administrator gets back to the
 * Features page after switching something off. `/docs` is *not* one of those
 * escape hatches: its API answers 404 once `guides` is off, so leaving the page
 * and its sidebar link in place only produced a load error.
 *
 * Note that `webhook_triggers` and `cron_triggers` gate *execution*, not
 * management, so the Webhooks and Schedules pages map to the builder feature:
 * their records stay editable while they are not firing.
 */
export const routeFeatures: Record<string, string> = {
  '/providers': FEATURE_PROVIDER_SETUP,
  '/pricing': FEATURE_MODEL_PRICING,
  '/usage': FEATURE_USAGE_ANALYTICS,
  '/settings/tokens': FEATURE_API_TOKENS,
  '/routing-profiles': FEATURE_ROUTING_PROFILES,
  '/playground': FEATURE_PLAYGROUND,
  '/sessions': FEATURE_CHAT_SESSIONS,
  '/bots': FEATURE_BOTS,
  '/agents': FEATURE_AGENTS,
  '/skills': FEATURE_SKILLS,
  '/marketplaces': FEATURE_MARKETPLACES,
  '/docs': FEATURE_GUIDES,
  '/variables': FEATURE_VARIABLES,
  '/node-configs': FEATURE_WORKFLOW_BUILDER,
  '/workflows': FEATURE_WORKFLOW_BUILDER,
  '/webhooks': FEATURE_WORKFLOW_BUILDER,
  '/crons': FEATURE_WORKFLOW_BUILDER,
  '/runs': FEATURE_WORKFLOW_RUNS,
  '/connections': FEATURE_EXTERNAL_CONNECTIONS,
  '/integrations': FEATURE_INTEGRATION_PACKS,
  '/mcp-servers': FEATURE_MCP_SERVERS,
  '/mcps': FEATURE_MCP_SERVERS,
  '/terminal': FEATURE_TERMINAL,
  '/files': FEATURE_FILES,
  '/organizations': FEATURE_ORGANIZATIONS,
  '/tasks': FEATURE_TASKS,
  '/goals': FEATURE_GOALS_PROJECTS,
  '/studio': FEATURE_STUDIO,
  '/llm-calls': FEATURE_LLM_TRACES,
};

const orderedPrefixes = Object.keys(routeFeatures).sort((a, b) => b.length - a.length);

export function routeFeature(route: string): string {
  const prefix = orderedPrefixes.find((p) => route === p || route.startsWith(p + '/'));
  return prefix ? routeFeatures[prefix] : '';
}

export function routeFeatureEnabled(route: string): boolean {
  const feature = routeFeature(route);
  return !feature || isFeatureEnabled(feature);
}
