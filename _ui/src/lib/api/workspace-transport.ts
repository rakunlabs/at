import axios, { type AxiosAdapter } from 'axios';
export function createWorkspaceTransport(baseURI: string, initial = '') {
  const base = new URL('.', baseURI);
  let selected = initial;
  let generation = 0;
  let controller = new AbortController();
  const scoped = (value: string) => {
    const url = new URL(value, base);
    if (url.origin !== base.origin || !url.pathname.startsWith(base.pathname + 'api/v1/')) return false;
    const path = url.pathname.slice((base.pathname + 'api/v1/').length);
    return !/^(features|settings|admin|health)(\/|$)/.test(path) && path !== 'workspaces';
  };
  return {
    get selected() { return selected; },
    select(id: string) { generation++; controller.abort(); controller = new AbortController(); selected = id; },
    wrapAdapter(adapter: AxiosAdapter): AxiosAdapter {
      return async config => {
        if (!scoped(axios.getUri(config))) return adapter(config);
        const start = generation;
        config.headers.delete('X-AT-Workspace-ID');
        if (selected) config.headers.set('X-AT-Workspace-ID', selected);
        config.signal = AbortSignal.any([controller.signal, ...(config.signal ? [config.signal as AbortSignal] : [])]);
        const result = await adapter(config);
        if (start !== generation) throw new axios.CanceledError('Workspace changed');
        return result;
      };
    },
    wrapFetch(fetcher: typeof fetch): typeof fetch {
      return async (input, init) => {
        const url = input instanceof Request ? input.url : new URL(String(input), base).href;
        if (!scoped(url)) return fetcher(input, init);
        const start = generation;
        const request = new Request(input instanceof Request ? input : url, init);
        const headers = new Headers(request.headers); headers.delete('X-AT-Workspace-ID');
        if (selected) headers.set('X-AT-Workspace-ID', selected);
        const response = await fetcher(new Request(request, { headers, signal: AbortSignal.any([controller.signal, request.signal]) }));
        if (start !== generation) { void response.body?.cancel(); throw new DOMException('Workspace changed', 'AbortError'); }
        return response;
      };
    },
  };
}
