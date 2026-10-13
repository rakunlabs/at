// A tool that creates something which runs unattended (a cron trigger)
// reports `binding_required` in its result. Chat surfaces turn that into an
// approval card; the approval is the person's own authenticated request.

export type ApprovalKind = 'bot' | 'mcp' | 'trigger';

export interface BindingRequest {
  kind: ApprovalKind;
  subjectId: string;
  label: string;
}

const kinds: ApprovalKind[] = ['bot', 'mcp', 'trigger'];

export function bindingRequestFromResult(text: string): BindingRequest | null {
  const trimmed = (text || '').trim();
  if (!trimmed.startsWith('{') || !trimmed.includes('binding_required')) return null;
  let parsed: any;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return null;
  }
  const req = parsed?.binding_required;
  if (!req || typeof req !== 'object') return null;
  const kind = req.kind as ApprovalKind;
  const subjectId = typeof req.subject_id === 'string' ? req.subject_id : '';
  if (!kinds.includes(kind) || !subjectId) return null;
  const schedule = typeof parsed?.config?.schedule === 'string' ? parsed.config.schedule : '';
  const title = typeof parsed?.config?.task_title === 'string' ? parsed.config.task_title : '';
  const alias = typeof parsed?.alias === 'string' ? parsed.alias : '';
  const name = typeof parsed?.name === 'string' ? parsed.name : typeof parsed?.platform === 'string' ? parsed.platform : '';
  const label = [title || alias || name, schedule ? `(${schedule})` : ''].filter(Boolean).join(' ') || subjectId;
  return { kind, subjectId, label };
}
