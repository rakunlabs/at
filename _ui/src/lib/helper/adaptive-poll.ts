/** One request at a time; back off on unchanged data/errors, suspend offline. */
export function createAdaptivePoll(options: {
  active(): boolean;
  poll(): Promise<boolean>;
  minimum?: number;
  maximum?: number;
}) {
  const minimum = options.minimum ?? 3000;
  const maximum = options.maximum ?? 30000;
  let delay = minimum;
  let stopped = false;
  let running = false;
  let wakeRequested = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const schedule = () => { if (!stopped) timer = setTimeout(run, delay); };
  async function run() {
    if (stopped || running) return;
    running = true;
    try {
      if (options.active()) delay = await options.poll() ? minimum : Math.min(maximum, delay * 2);
    } catch { delay = Math.min(maximum, delay * 2); }
    finally {
      running = false;
      if (wakeRequested) { delay = minimum; wakeRequested = false; }
      schedule();
    }
  }
  schedule();
  return {
    wake() {
      if (stopped) return;
      delay = minimum;
      if (running) { wakeRequested = true; return; }
      clearTimeout(timer);
      void run();
    },
    stop() { stopped = true; clearTimeout(timer); },
  };
}
