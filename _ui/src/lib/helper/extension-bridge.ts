/**
 * The AT extension bridge: a generic channel between this page and whatever
 * browser extensions the person has installed.
 *
 * Every other tool source in Chats is dialled over HTTP — by the server for
 * MCP sets and skills, by this page for local MCP servers. Neither can reach a
 * browser extension: an extension has no URL, and the APIs that make one worth
 * talking to (tabs, downloads, debugger, the DOM of *other* pages) exist only
 * inside the browser. The single channel a page and an extension already share
 * is `window.postMessage` through the extension's content script, so that is
 * what this module speaks.
 *
 * It is deliberately **not** written for one extension. The page broadcasts a
 * `describe` request; every extension implementing this protocol answers with
 * its own id, name and capabilities, and tools are listed and called per id.
 * Adding a second extension needs no change here, which is the whole reason
 * the discovery step exists rather than a hardcoded handshake with one vendor.
 *
 * **This is not a security boundary between extensions.** Any content script on
 * this page can post any message, so a hostile extension can claim another's
 * id. What actually gates it is that the person installed the extension, that
 * the extension only answers origins its user connected it to, and the
 * per-device approval below. Treat an extension like a program on the machine,
 * because that is what it is.
 *
 * Runtime imports are forbidden: `tests/extension-bridge.test.mjs` compiles
 * this single file to a data URL. Type-only imports are fine.
 */

/** Channel discriminator. Present on every message in both directions. */
export const EXTENSION_BRIDGE_CHANNEL = 'at.extension.bridge';

/** Protocol revision. An extension must echo the version it answers. */
export const EXTENSION_BRIDGE_VERSION = 1;

/** Declared by an extension that serves `tools/list` and `tools/call`. */
export const CAPABILITY_TOOLS = 'tools';

export const METHOD_DESCRIBE = 'describe';
export const METHOD_LIST_TOOLS = 'tools/list';
export const METHOD_CALL_TOOL = 'tools/call';

/**
 * Bounds. An extension is third-party code that chooses these strings, so each
 * one is clamped rather than trusted: a thousand tools or a megabyte of
 * description would be spent on the model's context, not on the page.
 */
const MAX_EXTENSIONS = 8;
const MAX_TOOLS = 128;
const MAX_ID_LENGTH = 64;
const MAX_NAME_LENGTH = 64;
const MAX_DESCRIPTION_LENGTH = 2048;
const MAX_CAPABILITIES = 16;

/**
 * Discovery is a broadcast with no single answer, so it ends on a deadline.
 *
 * Generous because an extension's background worker may be asleep: MV3 evicts
 * it aggressively, and a cold wake before it can answer is ordinary. Too short
 * a window reports "none found" for an extension that is installed, connected
 * and about to reply, which is the one wrong answer this screen can give.
 */
export const DISCOVERY_TIMEOUT_MS = 1200;
/** Per-request deadline. An extension that never answers must not hang a turn. */
export const REQUEST_TIMEOUT_MS = 30_000;

export interface ExtensionDescriptor {
  /** Stable, extension-chosen. Identifies it across reloads and in approvals. */
  id: string;
  name: string;
  version: string;
  capabilities: string[];
  description: string;
  /**
   * Extension-supplied explanation shown verbatim when it is reachable but not
   * ready — "no tab connected", "sign in first". Without it a connected
   * extension offering nothing is indistinguishable from a broken one.
   */
  notice: string;
}

export interface ExtensionTool {
  name: string;
  description: string;
  inputSchema?: Record<string, unknown>;
}

export type ExtensionEventName = 'announce' | 'tools_changed' | 'goodbye';

export interface ExtensionEvent {
  extension: string;
  event: ExtensionEventName;
}

export type ExtensionFailure = 'unavailable' | 'timeout' | 'protocol' | 'tool';

export class ExtensionBridgeError extends Error {
  kind: ExtensionFailure;

  constructor(kind: ExtensionFailure, message: string) {
    super(message);
    this.name = 'ExtensionBridgeError';
    this.kind = kind;
  }
}

/**
 * The slice of `window` this needs. Narrow on purpose: the test harness passes
 * a plain object, and a module that only posts and listens should not be able
 * to reach anything else on the real window.
 */
export interface BridgeWindow {
  addEventListener(type: 'message', handler: (event: BridgeMessageEvent) => void): void;
  removeEventListener(type: 'message', handler: (event: BridgeMessageEvent) => void): void;
  postMessage(message: unknown, targetOrigin: string): void;
}

export interface BridgeMessageEvent {
  source?: unknown;
  origin?: string;
  data?: unknown;
}

export interface ExtensionBridgeOptions {
  window: BridgeWindow;
  /**
   * The page's own origin. Messages from any other origin are ignored and
   * nothing is posted to another one — a content script relaying into the page
   * posts with the page's origin, so this costs nothing and refuses a framed
   * or forged sender.
   */
  origin: string;
  discoveryTimeoutMs?: number;
  requestTimeoutMs?: number;
}

interface Pending {
  /** A broadcast collects until its deadline; a targeted call takes the first answer. */
  broadcast: boolean;
  extension: string;
  collected: unknown[];
  /** Both settle the entry: calling either twice is a no-op. */
  resolve(value: unknown): void;
  reject(error: unknown): void;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function text(value: unknown, max: number): string {
  return typeof value === 'string' ? value.trim().slice(0, max) : '';
}

/** Accepted only for `describe`: the id is a map key and an approval key. */
function identifier(value: unknown): string {
  const raw = text(value, MAX_ID_LENGTH);

  return /^[a-zA-Z0-9][a-zA-Z0-9._-]*$/.test(raw) ? raw : '';
}

/**
 * Reads one descriptor out of whatever the extension sent.
 *
 * Returns `null` rather than throwing on a malformed answer: one extension
 * replying with nonsense must not hide the others that answered correctly.
 */
export function parseDescriptor(value: unknown): ExtensionDescriptor | null {
  if (!isRecord(value)) return null;
  const id = identifier(value.id);
  if (!id) return null;
  const capabilities = Array.isArray(value.capabilities)
    ? value.capabilities
        .map(entry => text(entry, MAX_NAME_LENGTH))
        .filter(Boolean)
        .slice(0, MAX_CAPABILITIES)
    : [];

  return {
    id,
    // An extension that reports no name is still usable; its id is a name a
    // person can at least recognise, and a blank row is not.
    name: text(value.name, MAX_NAME_LENGTH) || id,
    version: text(value.version, MAX_NAME_LENGTH),
    capabilities,
    description: text(value.description, MAX_DESCRIPTION_LENGTH),
    notice: text(value.notice, MAX_DESCRIPTION_LENGTH),
  };
}

/** Same tolerance as `parseDescriptor`: bad entries are dropped, not fatal. */
export function parseTools(value: unknown): ExtensionTool[] {
  const raw = isRecord(value) && Array.isArray(value.tools) ? value.tools : [];
  const tools: ExtensionTool[] = [];
  const seen = new Set<string>();
  for (const entry of raw) {
    if (tools.length >= MAX_TOOLS) break;
    if (!isRecord(entry)) continue;
    const name = identifier(entry.name);
    if (!name || seen.has(name)) continue;
    seen.add(name);
    tools.push({
      name,
      description: text(entry.description, MAX_DESCRIPTION_LENGTH),
      inputSchema: isRecord(entry.inputSchema) ? entry.inputSchema : undefined,
    });
  }

  return tools;
}

/**
 * Flattens an MCP-shaped tool result to the text the transcript carries.
 *
 * Accepts a bare string too: the protocol is meant to be cheap to implement,
 * and an extension returning `"done"` should not have to learn the content
 * block shape to be correct.
 */
export function parseToolResult(value: unknown): { text: string; isError: boolean } {
  if (typeof value === 'string') return { text: value, isError: false };
  if (!isRecord(value)) return { text: '', isError: false };
  const isError = value.isError === true;
  const content = Array.isArray(value.content) ? value.content : [];
  const parts: string[] = [];
  for (const part of content) {
    if (typeof part === 'string') {
      parts.push(part);
      continue;
    }
    if (isRecord(part) && typeof part.text === 'string') parts.push(part.text);
  }
  if (parts.length === 0 && typeof value.text === 'string') parts.push(value.text);

  return { text: parts.join('\n'), isError };
}

export class ExtensionBridge {
  private readonly win: BridgeWindow;
  private readonly origin: string;
  private readonly discoveryTimeoutMs: number;
  private readonly requestTimeoutMs: number;
  private readonly pending = new Map<string, Pending>();
  private readonly listeners = new Set<(event: ExtensionEvent) => void>();
  private readonly onMessage: (event: BridgeMessageEvent) => void;
  private counter = 0;
  private disposed = false;

  constructor(options: ExtensionBridgeOptions) {
    this.win = options.window;
    this.origin = options.origin;
    this.discoveryTimeoutMs = options.discoveryTimeoutMs ?? DISCOVERY_TIMEOUT_MS;
    this.requestTimeoutMs = options.requestTimeoutMs ?? REQUEST_TIMEOUT_MS;
    this.onMessage = event => this.receive(event);
    this.win.addEventListener('message', this.onMessage);
    this.announce('announce');
  }

  private announce(event: 'announce' | 'goodbye'): void {
    if (!this.origin) return;
    this.win.postMessage({
      channel: EXTENSION_BRIDGE_CHANNEL, v: EXTENSION_BRIDGE_VERSION,
      dir: 'agent', event, name: 'AT Chat',
    }, this.origin);
  }

  /** Stops listening. Pending calls are rejected rather than left hanging. */
  dispose(): void {
    if (this.disposed) return;
    this.announce('goodbye');
    this.disposed = true;
    this.win.removeEventListener('message', this.onMessage);
    // `resolve` / `reject` settle the entry themselves, so the map is walked
    // over a copy and never mutated underneath the loop.
    for (const [, pending] of [...this.pending]) {
      if (pending.broadcast) pending.resolve(pending.collected);
      else pending.reject(new ExtensionBridgeError('unavailable', 'the page stopped listening'));
    }
    this.listeners.clear();
  }

  /** Announcements, tool-list changes and departures. Returns an unsubscribe. */
  subscribe(handler: (event: ExtensionEvent) => void): () => void {
    this.listeners.add(handler);

    return () => this.listeners.delete(handler);
  }

  /**
   * Broadcasts `describe` and returns everything that answered before the
   * deadline.
   *
   * An extension that has not been connected to this origin is expected to
   * stay silent, so "none found" legitimately means both "nothing installed"
   * and "nothing connected here" — the page must not claim to know which.
   */
  async discover(): Promise<ExtensionDescriptor[]> {
    const answers = await this.send('', METHOD_DESCRIBE, undefined, {
      broadcast: true,
      timeoutMs: this.discoveryTimeoutMs,
    });
    const found: ExtensionDescriptor[] = [];
    const seen = new Set<string>();
    for (const answer of answers as unknown[]) {
      if (found.length >= MAX_EXTENSIONS) break;
      const descriptor = parseDescriptor(answer);
      // First answer wins: a later message cannot redefine an id already shown.
      if (!descriptor || seen.has(descriptor.id)) continue;
      seen.add(descriptor.id);
      found.push(descriptor);
    }

    return found;
  }

  async listTools(extension: string): Promise<ExtensionTool[]> {
    return parseTools(await this.send(extension, METHOD_LIST_TOOLS));
  }

  async callTool(
    extension: string,
    name: string,
    args: Record<string, unknown>,
    signal?: AbortSignal,
  ): Promise<string> {
    const result = await this.send(
      extension,
      METHOD_CALL_TOOL,
      { name, arguments: args ?? {} },
      { signal },
    );
    const { text: output, isError } = parseToolResult(result);
    // A tool that reports failure is an error the model should see, not a
    // string that reads like success.
    if (isError) throw new ExtensionBridgeError('tool', output || `${name} reported an error`);

    return output;
  }

  private send(
    extension: string,
    method: string,
    params?: unknown,
    options: { broadcast?: boolean; timeoutMs?: number; signal?: AbortSignal } = {},
  ): Promise<unknown> {
    if (this.disposed) {
      return Promise.reject(new ExtensionBridgeError('unavailable', 'the bridge is closed'));
    }
    if (!this.origin) {
      // Without a known origin neither the send nor the accept check can be
      // made safely, and guessing is how a page starts answering strangers.
      return Promise.reject(new ExtensionBridgeError('unavailable', 'this page has no verifiable origin'));
    }

    const broadcast = options.broadcast === true;
    this.counter += 1;
    const id = `at-${Date.now().toString(36)}-${this.counter}`;

    return new Promise<unknown>((resolve, reject) => {
      // Held here rather than read back out of the map: a broadcast resolves
      // with what it collected, and the map entry is gone by the time the
      // deadline runs.
      const collected: unknown[] = [];
      const signal = options.signal;

      // `delete` reports whether the entry was still live, which makes settling
      // idempotent: an answer arriving as the deadline fires resolves once.
      const settle = (finish: () => void) => {
        if (!this.pending.delete(id)) return;
        clearTimeout(timer);
        signal?.removeEventListener('abort', onAbort);
        finish();
      };
      const onAbort = () => settle(() => reject(new ExtensionBridgeError('unavailable', 'cancelled')));

      const timer = setTimeout(() => {
        settle(() => {
          if (broadcast) resolve(collected);
          else {
            reject(
              new ExtensionBridgeError(
                'timeout',
                `${extension || 'the extension'} did not answer ${method} in time`,
              ),
            );
          }
        });
      }, options.timeoutMs ?? this.requestTimeoutMs);

      this.pending.set(id, {
        broadcast,
        extension,
        collected,
        resolve: value => settle(() => resolve(value)),
        reject: error => settle(() => reject(error)),
      });

      if (signal) {
        if (signal.aborted) {
          onAbort();

          return;
        }
        signal.addEventListener('abort', onAbort, { once: true });
      }

      const message: Record<string, unknown> = {
        channel: EXTENSION_BRIDGE_CHANNEL,
        v: EXTENSION_BRIDGE_VERSION,
        dir: 'request',
        id,
        method,
      };
      if (extension) message.extension = extension;
      if (params !== undefined) message.params = params;

      try {
        this.win.postMessage(message, this.origin);
      } catch (error) {
        settle(() =>
          reject(new ExtensionBridgeError('unavailable', (error as Error)?.message || 'could not post to the page')),
        );
      }
    });
  }

  private receive(event: BridgeMessageEvent): void {
    if (this.disposed) return;
    // Same two checks the relaying content script makes in the other
    // direction: this document's own window, and this document's origin.
    if (event.source !== this.win) return;
    if (event.origin !== this.origin) return;
    const data = event.data;
    if (!isRecord(data)) return;
    if (data.channel !== EXTENSION_BRIDGE_CHANNEL) return;
    if (data.v !== EXTENSION_BRIDGE_VERSION) return;

    // A newly installed/restarted extension can discover an already-open chat.
    if (data.dir === 'agent' && data.event === 'discover') {
      this.announce('announce');
      return;
    }

    if (data.dir === 'event') {
      const extension = identifier(data.extension);
      const name = data.event;
      if (!extension) return;
      if (name !== 'announce' && name !== 'tools_changed' && name !== 'goodbye') return;
      for (const listener of [...this.listeners]) listener({ extension, event: name });

      return;
    }

    if (data.dir !== 'response') return;
    const id = typeof data.id === 'string' ? data.id : '';
    const pending = this.pending.get(id);
    if (!pending) return;
    const extension = identifier(data.extension);

    if (pending.broadcast) {
      // Collect and keep waiting: the other extensions are still entitled to
      // the rest of the window. Bounded because nothing else stops one sender
      // from filling the array for the whole deadline.
      if (isRecord(data.result) && pending.collected.length < MAX_EXTENSIONS * 4) {
        // The envelope names the sender and is what later targeted calls are
        // addressed to, so it wins; a descriptor that omits its own id is
        // completed from it rather than discarded.
        pending.collected.push({ ...data.result, id: extension || data.result.id });
      }

      return;
    }

    if (extension && pending.extension && extension !== pending.extension) return;
    if (isRecord(data.error) || typeof data.error === 'string') {
      const message = isRecord(data.error) ? text(data.error.message, MAX_DESCRIPTION_LENGTH) : String(data.error);
      pending.reject(new ExtensionBridgeError('tool', message || 'the extension reported an error'));

      return;
    }
    pending.resolve(data.result);
  }
}

/**
 * Approval is per device and per extension, held in local storage rather than
 * on the account.
 *
 * The same extension id is a different program on a laptop and on a desktop —
 * the person installed each one separately — so a synced approval would
 * authorize, on the second machine, something inspected on the first. This is
 * the same reasoning as the local-MCP approvals and deliberately a separate
 * key space: revoking one must not revoke the other.
 */
const APPROVAL_KEY = 'at.extensions.approved';

export interface ExtensionApproval {
  /** Tool names present at approval time, so later additions can be labelled. */
  tools: string[];
  at: string;
}

type ApprovalMap = Record<string, ExtensionApproval>;

function readApprovals(storage: Storage | undefined): ApprovalMap {
  if (!storage) return {};
  try {
    const raw = storage.getItem(APPROVAL_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw);

    return parsed && typeof parsed === 'object' ? (parsed as ApprovalMap) : {};
  } catch {
    return {};
  }
}

function writeApprovals(storage: Storage | undefined, map: ApprovalMap): void {
  if (!storage) return;
  try {
    storage.setItem(APPROVAL_KEY, JSON.stringify(map));
  } catch {
    // Full or disabled storage means the approval does not persist and the
    // person is asked again, which is the safe direction to fail in.
  }
}

export function extensionApprovalFor(id: string, storage: Storage | undefined): ExtensionApproval | null {
  return readApprovals(storage)[id] ?? null;
}

export function approveExtension(id: string, tools: string[], storage: Storage | undefined): void {
  const map = readApprovals(storage);
  map[id] = { tools: [...tools], at: new Date().toISOString() };
  writeApprovals(storage, map);
}

export function revokeExtension(id: string, storage: Storage | undefined): void {
  const map = readApprovals(storage);
  delete map[id];
  writeApprovals(storage, map);
}
