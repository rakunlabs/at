/** Keep writes ordered; a failed operation must not poison later work. */
export function createSerialQueue() {
  let tail: Promise<unknown> | null = null;
  return {
    run<T>(operation: () => Promise<T>): Promise<T> {
      // Start the first operation synchronously (claiming UI state before await).
      const result = tail ? tail.then(operation) : (async () => operation())();
      const settled = result.catch(() => {});
      tail = settled;
      void settled.then(() => { if (tail === settled) tail = null; });
      return result;
    },
  };
}
