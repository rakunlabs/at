interface Timer {
  set(callback: () => void, delay: number): ReturnType<typeof setTimeout>;
  clear(handle: ReturnType<typeof setTimeout>): void;
}

/** Capture at scheduling time; navigation must not change a pending payload. */
export function createDebouncedSave<T>(write: (value: T) => Promise<void>, delay: number, timer: Timer = {
  set: (callback, ms) => setTimeout(callback, ms),
  clear: handle => clearTimeout(handle),
}) {
  let pending: { value: T } | null = null;
  let handle: ReturnType<typeof setTimeout> | null = null;

  function cancel() {
    if (handle !== null) timer.clear(handle);
    handle = null;
    pending = null;
  }

  async function flush() {
    const next = pending;
    cancel();
    if (next) await write(next.value);
  }

  return {
    schedule(value: T) {
      cancel();
      pending = { value };
      handle = timer.set(() => { void flush(); }, delay);
    },
    flush,
    cancel,
  };
}
