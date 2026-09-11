export function takeRecoveryTicket(location: Pick<Location, 'hash' | 'pathname' | 'search'>, history: Pick<History, 'replaceState'>): string {
  if (!location.hash.startsWith('#recovery=')) return '';
  const fragment = location.hash.slice(1);
  // Scrub before decoding, validation, rendering, or any network request.
  history.replaceState(null, '', location.pathname + location.search);
  const params = new URLSearchParams(fragment);
  return params.getAll('recovery').length === 1 ? params.get('recovery') || '' : '';
}
export function downloadSecret(filename: string, contents: string) {
  const url = URL.createObjectURL(new Blob([contents], { type: 'text/plain;charset=utf-8' }));
  const anchor = document.createElement('a'); anchor.href = url; anchor.download = filename; anchor.click();
  window.setTimeout(() => URL.revokeObjectURL(url), 1000);
}
