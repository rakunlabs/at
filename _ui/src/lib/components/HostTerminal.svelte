<script lang="ts">
  import { onMount } from 'svelte';
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import '@xterm/xterm/css/xterm.css';
  import { terminalSocketURL, type TerminalAppearance } from '@/lib/api/terminals';

  interface Props {
    id: string;
    // Palette choice. "system" follows the page theme; the default keeps the
    // terminal dark while the rest of the UI stays light.
    appearance?: TerminalAppearance;
    // Font family resolved on this device. Missing fonts fall back silently.
    fontFamily?: string;
    fontSize?: number;
    onstatus: (status: string, message: string) => void;
  }
  let { id, appearance = 'dark', fontFamily = '', fontSize = 14, onstatus }: Props = $props();
  let container: HTMLDivElement;
  let term = $state.raw<Terminal | null>(null);
  let refit: () => void = () => {};

  const DARK = { background: '#17191c', foreground: '#e5e7eb', cursor: '#e5e7eb', selectionBackground: '#4b5563' };
  const LIGHT = { background: '#ffffff', foreground: '#1f2937', cursor: '#1f2937', selectionBackground: '#cbd5e1' };
  const FALLBACK = 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace';

  // Nerd Font glyphs come from the device running this browser, not the host, so
  // the chosen family is always followed by the built-in stack.
  function cssFamily(value: string): string {
    const names = value.split(',').map(part => part.trim().replace(/["']/g, '')).filter(Boolean);
    return [...names.map(name => `"${name}"`), FALLBACK].join(', ');
  }

  let pageDark = $state(document.documentElement.classList.contains('dark'));
  let theme = $derived(appearance === 'light' || (appearance === 'system' && !pageDark) ? LIGHT : DARK);
  let family = $derived(cssFamily(fontFamily));
  let size = $derived(Math.min(28, Math.max(10, Math.round(fontSize) || 14)));

  // Font metrics and palette are live options: apply them to the running
  // terminal and refit so the host learns the new column and row count.
  $effect(() => {
    const active = term;
    const options = { theme, fontFamily: family, fontSize: size };
    if (!active) return;
    active.options.theme = options.theme;
    active.options.fontFamily = options.fontFamily;
    active.options.fontSize = options.fontSize;
    refit();
  });

  onMount(() => {
    let disposed = false;
    let ready = false;
    let failed = false;
    let pendingBytes = 0;
    const instance = new Terminal({ cursorBlink: true, fontSize: size, fontFamily: family, scrollback: 2000, theme, screenReaderMode: true });
    const fit = new FitAddon();
    instance.loadAddon(fit);
    instance.open(container);
    instance.textarea?.setAttribute('aria-label', 'Host terminal input');
    const ws = new WebSocket(terminalSocketURL(id));
    ws.binaryType = 'arraybuffer';
    onstatus('connecting', 'Connecting to host…');

    function resize() {
      if (disposed || container.clientWidth < 30 || container.clientHeight < 30) return;
      fit.fit();
      const cols = Math.max(2, Math.min(500, instance.cols));
      const rows = Math.max(2, Math.min(300, instance.rows));
      if (ready && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'resize', cols, rows }));
    }
    refit = resize;
    const observer = new ResizeObserver(resize);
    observer.observe(container);
    const themeObserver = new MutationObserver(() => { pageDark = document.documentElement.classList.contains('dark'); });
    themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
    const input = instance.onData((value) => {
      if (!ready || ws.readyState !== WebSocket.OPEN) return;
      const data = new TextEncoder().encode(value);
      if (ws.bufferedAmount + data.length > 1024 * 1024) {
        failed = true;
        onstatus('error', 'Input is arriving faster than the connection can send it. Reconnect and paste a smaller block.');
        ws.close();
        return;
      }
      for (let offset = 0; offset < data.length; offset += 8192) ws.send(data.slice(offset, offset + 8192));
    });
    ws.onmessage = (event) => {
      if (disposed) return;
      if (event.data instanceof ArrayBuffer) {
        pendingBytes += event.data.byteLength;
        if (pendingBytes > 2 * 1024 * 1024) {
          failed = true;
          onstatus('error', 'Output exceeded the display buffer. The shell is still running; reconnect to resume.');
          ws.close();
          return;
        }
        const length = event.data.byteLength;
        instance.write(new Uint8Array(event.data), () => { pendingBytes -= length; });
        return;
      }
      try {
        const message = JSON.parse(event.data);
        if (message.type === 'ready') {
          ready = true;
          resize();
          instance.focus();
          onstatus('connected', 'Connected');
        } else {
          failed = true;
          ready = false;
          onstatus(message.type === 'error' ? 'error' : 'disconnected', message.message || 'Disconnected');
        }
      } catch { failed = true; onstatus('error', 'Invalid terminal response'); ws.close(); }
    };
    ws.onerror = () => { if (!disposed) { failed = true; onstatus('error', 'Connection failed. Check that the host is online and your sign-in is still valid.'); } };
    ws.onclose = () => { ready = false; if (!disposed && !failed) onstatus('disconnected', 'Connection closed. Your shell stays on the host. Reconnect to resume.'); };
    resize();
    term = instance;
    return () => {
      disposed = true;
      term = null;
      refit = () => {};
      ws.close();
      observer.disconnect();
      themeObserver.disconnect();
      input.dispose();
      instance.dispose();
    };
  });
</script>

<div bind:this={container} class="h-full min-h-0 w-full overflow-hidden p-2" style:background={theme.background} aria-label="Interactive Linux terminal"></div>
