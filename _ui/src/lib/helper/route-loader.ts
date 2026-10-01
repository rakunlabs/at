/** Keep successful imports stable across parameter changes. A failed download
 * is not cached: an offline visit must not poison the route for this tab. */
export function createRouteLoader<T>(load: () => Promise<T>, fallback: (error: unknown) => T) {
  let pending: Promise<T> | undefined;
  return () => {
    if (!pending) {
      pending = Promise.resolve().then(load).catch(error => {
        pending = undefined;
        return fallback(error);
      });
    }
    return pending;
  };
}
