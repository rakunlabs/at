import {
  ArrowRightFromLine, AudioLines, Binary, Bot, Braces, CalendarClock, CodeXml, Combine, DoorOpen, FileText,
  Funnel, GitBranch, Globe, Group, Hourglass, LogIn, Mail, Mic, PencilLine, Plug, Repeat, Route, ScrollText,
  Sigma, Sparkles, SquareTerminal, StickyNote, Target, UserCog, Webhook, Workflow, BookOpen,
} from 'lucide-svelte';

// Canvas cards, the palette and the step dialog share one icon and colour per
// node type. Colour follows what a step does (AI, integration, data, flow…),
// not the palette group, so related steps read alike on the canvas.
export type WorkflowNodeKind = 'entry' | 'ai' | 'integration' | 'code' | 'data' | 'flow' | 'media' | 'resource' | 'output' | 'note';

// Lucide's legacy component type does not match Svelte 5's Component<>, so
// the icon type is taken from lucide itself.
export interface WorkflowNodeAppearance {
  icon: typeof Braces;
  kind: WorkflowNodeKind;
}

export const workflowKindLabels: Record<WorkflowNodeKind, string> = {
  entry: 'Trigger',
  ai: 'AI',
  integration: 'Integration',
  code: 'Code',
  data: 'Data',
  flow: 'Flow',
  media: 'Media',
  resource: 'Resource',
  output: 'Output',
  note: 'Note',
};

// Full class strings so Tailwind can see them.
export const workflowKindTile: Record<WorkflowNodeKind, string> = {
  entry: 'bg-emerald-600 text-white',
  ai: 'bg-violet-600 text-white',
  integration: 'bg-sky-600 text-white',
  code: 'text-white bg-slate-500',
  data: 'bg-teal-600 text-white',
  flow: 'bg-amber-600 text-white',
  media: 'bg-orange-600 text-white',
  resource: 'bg-fuchsia-600 text-white',
  output: 'bg-rose-600 text-white',
  note: 'bg-yellow-500 text-white',
};

export const workflowKindStripe: Record<WorkflowNodeKind, string> = {
  entry: 'border-t-emerald-600',
  ai: 'border-t-violet-600',
  integration: 'border-t-sky-600',
  code: 'border-t-slate-500',
  data: 'border-t-teal-600',
  flow: 'border-t-amber-600',
  media: 'border-t-orange-600',
  resource: 'border-t-fuchsia-600',
  output: 'border-t-rose-600',
  note: 'border-t-yellow-500',
};

const appearances: Record<string, WorkflowNodeAppearance> = {
  input: { icon: LogIn, kind: 'entry' },
  http_trigger: { icon: Webhook, kind: 'entry' },
  cron_trigger: { icon: CalendarClock, kind: 'entry' },
  llm_call: { icon: Sparkles, kind: 'ai' },
  agent_call: { icon: Bot, kind: 'ai' },
  decision: { icon: Target, kind: 'ai' },
  embedding: { icon: Binary, kind: 'ai' },
  template: { icon: FileText, kind: 'code' },
  script: { icon: CodeXml, kind: 'code' },
  exec: { icon: SquareTerminal, kind: 'code' },
  workflow_call: { icon: Workflow, kind: 'integration' },
  http_request: { icon: Globe, kind: 'integration' },
  email: { icon: Mail, kind: 'integration' },
  log: { icon: ScrollText, kind: 'code' },
  audio_generate: { icon: AudioLines, kind: 'media' },
  audio_transcribe: { icon: Mic, kind: 'media' },
  edit_fields: { icon: PencilLine, kind: 'data' },
  filter: { icon: Funnel, kind: 'data' },
  aggregate: { icon: Sigma, kind: 'data' },
  conditional: { icon: GitBranch, kind: 'flow' },
  switch: { icon: Route, kind: 'flow' },
  merge: { icon: Combine, kind: 'flow' },
  loop: { icon: Repeat, kind: 'flow' },
  wait: { icon: Hourglass, kind: 'flow' },
  gate: { icon: DoorOpen, kind: 'flow' },
  skill_config: { icon: BookOpen, kind: 'resource' },
  agent_config: { icon: UserCog, kind: 'resource' },
  mcp_config: { icon: Plug, kind: 'resource' },
  output: { icon: ArrowRightFromLine, kind: 'output' },
  group: { icon: Group, kind: 'note' },
  sticky_note: { icon: StickyNote, kind: 'note' },
};

const fallback: WorkflowNodeAppearance = { icon: Braces, kind: 'code' };

export function getWorkflowNodeAppearance(type: string): WorkflowNodeAppearance {
  return appearances[type] ?? fallback;
}

export function modelLabel(provider?: string, model?: string): string {
  if (provider && model) return `${provider} / ${model}`;
  return model || provider || '';
}
