<script lang="ts">
  import { onMount } from 'svelte';
  import { Terminal } from '@xterm/xterm';
  import { FitAddon } from '@xterm/addon-fit';
  import '@xterm/xterm/css/xterm.css';
  import { terminalSocketURL } from '@/lib/api/terminals';

  interface Props { id: string; onstatus: (status: string, message: string) => void }
  let { id, onstatus }: Props = $props();
  let container: HTMLDivElement;

  onMount(() => {
    let disposed = false;
    let ready = false;
    let failed = false;
    let pendingBytes = 0;
    const theme = () => document.documentElement.classList.contains('dark')
      ? { background: '#17191c', foreground: '#e5e7eb', cursor: '#e5e7eb', selectionBackground: '#4b5563' }
      : { background: '#ffffff', foreground: '#1f2937', cursor: '#1f2937', selectionBackground: '#cbd5e1' };
    const term = new Terminal({ cursorBlink: true, fontSize: 14, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace', scrollback: 2000, theme: theme(), screenReaderMode: true });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(container);
    term.textarea?.setAttribute('aria-label', 'Host terminal input');
    const ws = new WebSocket(terminalSocketURL(id));
    ws.binaryType = 'arraybuffer';
    onstatus('connecting', 'Connecting to host…');

    function resize() {
      if (disposed || container.clientWidth < 30 || container.clientHeight < 30) return;
      fit.fit();
      const cols = Math.max(2, Math.min(500, term.cols));
      const rows = Math.max(2, Math.min(300, term.rows));
      if (ready && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify({ type: 'resize', cols, rows }));
    }
    const observer = new ResizeObserver(resize);
    observer.observe(container);
    const themeObserver = new MutationObserver(() => { term.options.theme = theme(); });
    themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] });
    const input = term.onData((value) => {
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
        term.write(new Uint8Array(event.data), () => { pendingBytes -= length; });
        return;
      }
      try {
        const message = JSON.parse(event.data);
        if (message.type === 'ready') {
          ready = true;
          resize();
          term.focus();
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
    return () => {
      disposed = true;
      ws.close();
      observer.disconnect();
      themeObserver.disconnect();
      input.dispose();
      term.dispose();
    };
  });
</script>

<div bind:this={container} class="h-full min-h-0 w-full overflow-hidden p-2" aria-label="Interactive Linux terminal"></div>
