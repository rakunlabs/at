import type { BuiltinToolDef } from '../api/mcp';

export const builtinFamilies = [
  { key: 'builtin_shell', label: 'Shell' },
  { key: 'builtin_script', label: 'Script' },
  { key: 'builtin_http', label: 'HTTP' },
  { key: 'builtin_other', label: 'Other built-in tools' },
];

export function builtinFamily(tool: BuiltinToolDef): string {
  if (tool.family) return tool.family;
  if (tool.name === 'bash_execute') return 'builtin_shell';
  if (tool.name === 'js_execute') return 'builtin_script';
  if (['http_request', 'url_fetch'].includes(tool.name)) return 'builtin_http';
  return 'builtin_other';
}

export function builtinDisabledBy(tool: BuiltinToolDef, enabled: (key: string) => boolean): string {
  if (!enabled('builtin_tools')) return 'builtin_tools';
  if (!enabled(builtinFamily(tool))) return builtinFamily(tool);
  if (tool.group && tool.group !== 'helpers' && !enabled(tool.group)) return tool.group;
  return tool.disabled_by || '';
}

export function builtinGroupLabel(key: string): string {
  return builtinFamilies.find(f => f.key === key)?.label
    || ({ files: 'Files', tasks: 'Tasks', organizations: 'Organizations', workflow_builder: 'Workflows',
      provider_setup: 'Providers', external_connections: 'Connections', mcp_servers: 'MCP',
      llm_traces: 'Traces', api_tokens: 'API tokens', helpers: 'Helpers' } as Record<string, string>)[key]
    || key.replaceAll('_', ' ').replace(/^./, c => c.toUpperCase());
}
