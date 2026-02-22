import yaml from 'js-yaml';
import type { LLMConfig } from '@/lib/api/providers';
import type { APIToken } from '@/lib/api/tokens';

/**
 * Build a clean config object from an LLMConfig, omitting empty/undefined fields
 * and replacing redacted api_key with a placeholder.
 */
function buildCleanConfig(config: LLMConfig): Record<string, unknown> {
  const clean: Record<string, unknown> = {};

  clean.type = config.type;

  if (config.api_key && config.api_key !== '***') {
    clean.api_key = config.api_key;
  } else if (config.api_key === '***') {
    clean.api_key = 'your-api-key-here';
  }

  if (config.base_url) {
    clean.base_url = config.base_url;
  }

  clean.model = config.model;

  if (config.models && config.models.length > 0) {
    clean.models = config.models;
  }

  if (config.extra_headers && Object.keys(config.extra_headers).length > 0) {
    clean.extra_headers = config.extra_headers;
  }

  if (config.auth_type) {
    clean.auth_type = config.auth_type;
  }

  if (config.proxy) {
    clean.proxy = config.proxy;
  }

  return clean;
}

/**
 * Generate a YAML config snippet for a provider, matching the at.yaml format:
 *
 *   providers:
 *     <key>:
 *       type: openai
 *       api_key: "..."
 *       model: "gpt-4o"
 */
export function generateYamlSnippet(key: string, config: LLMConfig): string {
  const obj = {
    providers: {
      [key]: buildCleanConfig(config),
    },
  };

  return yaml.dump(obj, {
    indent: 2,
    lineWidth: -1,
    quotingType: '"',
    forceQuotes: false,
    noRefs: true,
  }).trimEnd();
}

/**
 * Generate a JSON config snippet for a provider.
 */
export function generateJsonSnippet(key: string, config: LLMConfig): string {
  const obj = {
    providers: {
      [key]: buildCleanConfig(config),
    },
  };

  return JSON.stringify(obj, null, 2);
}

// ─── Auth Token Config Snippets ───

/**
 * Build a clean auth token config object from an APIToken, omitting
 * empty/undefined fields and using a placeholder for the token value.
 */
function buildCleanAuthToken(token: APIToken): Record<string, unknown> {
  const clean: Record<string, unknown> = {};

  clean.token = 'your-token-here';

  if (token.name) {
    clean.name = token.name;
  }

  if (token.allowed_providers && token.allowed_providers.length > 0) {
    clean.allowed_providers = token.allowed_providers;
  }

  if (token.allowed_models && token.allowed_models.length > 0) {
    clean.allowed_models = token.allowed_models;
  }

  if (token.expires_at) {
    clean.expires_at = token.expires_at;
  }

  return clean;
}

/**
 * Generate a YAML config snippet for an auth token, matching the at.yaml format:
 *
 *   gateway:
 *     auth_tokens:
 *       - token: "your-token-here"
 *         name: "My Token"
 *         allowed_providers:
 *           - openai
 */
export function generateAuthTokenYamlSnippet(token: APIToken): string {
  const obj = {
    gateway: {
      auth_tokens: [buildCleanAuthToken(token)],
    },
  };

  return yaml.dump(obj, {
    indent: 2,
    lineWidth: -1,
    quotingType: '"',
    forceQuotes: false,
    noRefs: true,
  }).trimEnd();
}

/**
 * Generate a JSON config snippet for an auth token.
 */
export function generateAuthTokenJsonSnippet(token: APIToken): string {
  const obj = {
    gateway: {
      auth_tokens: [buildCleanAuthToken(token)],
    },
  };

  return JSON.stringify(obj, null, 2);
}
