// Background mode runs a conversation's turn on the server. Everything the
// browser alone can reach is unavailable there; this module names it so the
// page can explain, rather than silently drop, what a run will not have.

export interface BackgroundSetup {
  model: string;
  frontend_tools: string[];
}

export interface BackgroundBrowserSources {
  /** Local MCP servers approved on this device. */
  localServers: string[];
  /** Approved browser extensions while the web connection is on. */
  extensions: string[];
}

/** Browser-only helper tools a background run cannot offer. */
export const BACKGROUND_UNSUPPORTED_FRONTEND_TOOLS: string[] = [];

export const QUESTION_TOOL = 'question';

export interface StoredQuestion {
  toolCallId: string;
  question: string;
  header?: string;
  options: Array<{ label: string; description?: string }>;
  multiple: boolean;
  custom: boolean;
}

interface TranscriptMessage {
  role: string;
  tool_calls?: Array<{ id: string; function: { name: string; arguments: string } }>;
  tool_call_id?: string;
}

/**
 * The question a background run is waiting on: a `question` call of the last
 * assistant step that has no result yet. Rebuilt from the transcript, so it
 * survives reloads, other devices and server restarts. Null when none.
 */
export function pendingStoredQuestion(messages: TranscriptMessage[]): StoredQuestion | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m.role === 'user') return null;
    if (m.role !== 'assistant' || !m.tool_calls?.length) continue;
    const answered = new Set(messages.slice(i + 1).filter(x => x.role === 'tool').map(x => x.tool_call_id));
    const call = m.tool_calls.find(c => c.function?.name === QUESTION_TOOL && !answered.has(c.id));
    if (!call) return null;
    let args: any = {};
    try { args = JSON.parse(call.function.arguments || '{}') ?? {}; } catch { /* shown with defaults */ }
    const options = Array.isArray(args.options)
      ? args.options.filter((o: any) => o && typeof o.label === 'string').map((o: any) => ({ label: o.label, ...(typeof o.description === 'string' ? { description: o.description } : {}) }))
      : [];
    return {
      toolCallId: call.id,
      question: typeof args.question === 'string' && args.question ? args.question : 'Please answer:',
      ...(typeof args.header === 'string' && args.header ? { header: args.header } : {}),
      options,
      multiple: args.multiple === true,
      custom: args.custom !== false,
    };
  }
  return null;
}

/** The tool result an answer is saved as — the same shape the browser loop returns. */
export function questionAnswerResult(answer: string): string {
  return JSON.stringify({ answer });
}

export function isBackgroundConfig(config: Record<string, unknown> | null | undefined): boolean {
  return config?.background === true;
}

/**
 * Why a conversation cannot run in the background right now, or '' when it
 * can. A local provider is called by this browser, so its turn cannot leave it.
 */
export function backgroundBlocker(setup: BackgroundSetup, isLocalModel: (model: string) => boolean): string {
  if (!setup.model) return 'Choose a model first.';
  if (isLocalModel(setup.model)) return 'Local providers are called by this browser and cannot run in the background. Choose a server model.';
  return '';
}

/** What the reader loses by running in the background, as short labels. */
export function backgroundOmissions(setup: BackgroundSetup, sources: BackgroundBrowserSources): string[] {
  const out: string[] = [];
  const frontend = setup.frontend_tools.filter(name => BACKGROUND_UNSUPPORTED_FRONTEND_TOOLS.includes(name));
  if (frontend.length) out.push(`browser tools: ${frontend.join(', ')}`);
  if (sources.localServers.length) out.push(`local MCP servers: ${sources.localServers.join(', ')}`);
  if (sources.extensions.length) out.push(`browser extensions: ${sources.extensions.join(', ')}`);
  return out;
}
