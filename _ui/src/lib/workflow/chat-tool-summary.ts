// Human-readable one-liners for the workflow assistant's canvas tools, so the
// transcript says what was changed and not only which tool ran.

function parseArgs(raw: string): Record<string, any> {
  try {
    const parsed = JSON.parse(raw || '{}');
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {};
  } catch {
    return {};
  }
}

function fieldList(data: unknown): string {
  if (!data || typeof data !== 'object' || Array.isArray(data)) return '';
  const keys = Object.keys(data);
  if (keys.length === 0) return '';
  const shown = keys.slice(0, 6).join(', ');
  return keys.length > 6 ? `${shown} +${keys.length - 6} more` : shown;
}

function point(p: any): string {
  if (!p || typeof p !== 'object') return '';
  const x = Number(p.x);
  const y = Number(p.y);
  return Number.isFinite(x) && Number.isFinite(y) ? `(${Math.round(x)}, ${Math.round(y)})` : '';
}

export function summarizeWorkflowToolCall(name: string, rawArgs: string): string {
  const args = parseArgs(rawArgs);
  switch (name) {
    case 'get_flow':
      return 'Read the current nodes and edges';
    case 'fit_view':
      return 'Zoomed the canvas to fit the whole workflow';
    case 'add_node': {
      const label = args.data?.label || args.data?.text || '';
      const fields = fieldList(args.data);
      const at = point(args.position);
      return [
        `Added ${args.type || 'node'}${label ? ` "${label}"` : ''}${args.id ? ` as ${args.id}` : ''}`,
        at && `at ${at}`,
        fields && `· set ${fields}`,
      ].filter(Boolean).join(' ');
    }
    case 'remove_node':
      return `Removed node ${args.id || '?'} and its edges`;
    case 'update_node_data': {
      const fields = fieldList(args.data);
      return `Updated ${args.id || '?'}${fields ? ` · set ${fields}` : ''}`;
    }
    case 'update_node_position':
      return `Moved ${args.id || '?'}${point(args.position) ? ` to ${point(args.position)}` : ''}`;
    case 'add_edge':
      return `Connected ${args.source || '?'}.${args.source_handle || '?'} → ${args.target || '?'}.${args.target_handle || '?'}`;
    case 'remove_edge':
      return `Removed edge ${args.id || '?'}`;
    default:
      return '';
  }
}
