/** Update hash-router query parameters without losing the route or other filters. */
export function updateRouteQuery(values: Record<string, string | null>, replace = false) {
  const hash = window.location.hash.slice(1) || '/';
  const index = hash.indexOf('?');
  const path = index < 0 ? hash : hash.slice(0, index);
  const query = new URLSearchParams(index < 0 ? '' : hash.slice(index + 1));
  for (const [key, value] of Object.entries(values)) {
    if (value === null || value === '') query.delete(key);
    else query.set(key, value);
  }
  const search = query.toString();
  const next = `#${path}${search ? `?${search}` : ''}`;
  if (next === window.location.hash) return;
  if (replace) {
    window.history.replaceState(window.history.state, '', next);
    window.dispatchEvent(new Event('hashchange'));
  } else {
    window.location.hash = next;
  }
}
