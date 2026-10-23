/**
 * Keeps a conversation's provider prompt cache warm while the reader is idle
 * (OpenCode's `warming`). Providers drop a cached prefix after a few minutes
 * (Anthropic: 5); a tiny request with the same prefix renews it, so the next
 * real message is billed at the cached rate and answers faster.
 *
 * After `interval` with no real request a warm request is sent, repeating
 * until `duration` has passed since the last real request. Warm requests do
 * not extend the window; real activity resets both timers. These are real,
 * billed requests (about seven per idle half hour with the defaults), which
 * is why it is opt-in per conversation.
 */
export const WARM_INTERVAL_MS = 4 * 60 * 1000;
export const WARM_DURATION_MS = 30 * 60 * 1000;

export const WARM_PROMPT = 'Keep-alive request to keep the prompt cache warm. Reply with only: ok';

export interface WarmerTimers {
  set(callback: () => void, ms: number): unknown;
  clear(handle: unknown): void;
  now(): number;
}

const browserTimers: WarmerTimers = {
  set: (callback, ms) => setTimeout(callback, ms),
  clear: handle => clearTimeout(handle as ReturnType<typeof setTimeout>),
  now: () => Date.now(),
};

export function createCacheWarmer(options: {
  /** Sends one warm request; failures are reported to onError, never thrown. */
  warm(): Promise<void>;
  /** Whether warming is allowed right now (enabled, idle, not hidden…). */
  allowed(): boolean;
  onError?(error: unknown): void;
  interval?: number;
  duration?: number;
  timers?: WarmerTimers;
}) {
  const interval = options.interval ?? WARM_INTERVAL_MS;
  const duration = options.duration ?? WARM_DURATION_MS;
  const timers = options.timers ?? browserTimers;
  let handle: unknown = null;
  let lastActivity = 0;
  let active = false;
  let inFlight = false;
  let stopped = false;

  const clear = () => {
    if (handle !== null) timers.clear(handle);
    handle = null;
  };
  const schedule = () => {
    clear();
    if (stopped || !active) return;
    if (timers.now() + interval - lastActivity > duration) return;
    handle = timers.set(tick, interval);
  };
  const tick = async () => {
    handle = null;
    if (stopped || !active) return;
    if (timers.now() - lastActivity >= duration) return;
    if (options.allowed() && !inFlight) {
      inFlight = true;
      try {
        await options.warm();
      } catch (error) {
        options.onError?.(error);
      } finally {
        inFlight = false;
      }
    }
    schedule();
  };

  return {
    /** A real request finished: (re)start the idle window. */
    activity() {
      lastActivity = timers.now();
      active = true;
      schedule();
    },
    /** Stop until the next activity (disabled, conversation changed). */
    reset() {
      clear();
      active = false;
    },
    destroy() {
      stopped = true;
      clear();
    },
  };
}
