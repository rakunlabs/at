import { createSerialQueue } from './serial-queue';

interface TranscriptWriter<Scope, Entry, Input, Stored> {
  current(scope: Scope): boolean;
  snapshot(scope: Scope): Entry[];
  prepare(entry: Entry): Promise<Input>;
  append(scope: Scope, inputs: Input[]): Promise<Stored[]>;
  adopt(entry: Entry, stored: Stored): void;
  busy(value: boolean): void;
  batchSize: number;
}

/** Snapshot before uploads; serialize appends so queued retries cannot duplicate rows. */
export function createTranscriptWriter<Scope, Entry, Input, Stored>(options: TranscriptWriter<Scope, Entry, Input, Stored>) {
  const queue = createSerialQueue();
  return {
    write(scope: Scope) {
      return queue.run(async () => {
        if (!options.current(scope)) return;
        const entries = options.snapshot(scope);
        if (!entries.length) return;
        options.busy(true);
        try {
          const inputs: Input[] = [];
          for (const entry of entries) {
            if (!options.current(scope)) return;
            inputs.push(await options.prepare(entry));
          }
          for (let offset = 0; offset < inputs.length; offset += options.batchSize) {
            if (!options.current(scope)) return;
            const stored = await options.append(scope, inputs.slice(offset, offset + options.batchSize));
            if (!options.current(scope)) return;
            stored.forEach((row, index) => options.adopt(entries[offset + index], row));
          }
        } finally { options.busy(false); }
      });
    },
  };
}
