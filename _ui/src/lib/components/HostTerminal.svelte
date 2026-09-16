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
    // Touch key row for keys a soft keyboard does not have.
    keyBar?: boolean;
    onstatus: (status: string, message: string) => void;
    // Reports whether this connection may type and how many others are attached.
    onrole?: (control: boolean, watchers: number) => void;
  }
  let { id, appearance = 'dark', fontFamily = '', fontSize = 14, keyBar = false, onstatus, onrole }: Props = $props();
  // The host is the authority on who may type; this only avoids sending
  // keystrokes that would be discarded, and greys out the key row.
  let control = $state(true);
  let container: HTMLDivElement;
  let term = $state.raw<Terminal | null>(null);
  let refit: () => void = () => {};
  let send: (data: string) => void = () => {};
  let resend: (frame: string) => void = () => {};
  let ctrlArmed = $state(false);

  // Soft keyboards have no Ctrl, Esc, Tab or arrows, so a phone cannot send an
  // interrupt, complete a path or drive a full-screen editor. These keys work on
  // the decoded input rather than keydown events, which mobile keyboards report
  // inconsistently: Ctrl is armed by tapping it, then folded into the next
  // character the keyboard produces.
  function applyCtrl(value: string): string {
    if (!ctrlArmed) return value;
    ctrlArmed = false;
    if (value.length !== 1) return value;
    const code = value.toUpperCase().charCodeAt(0);
    if (code === 32) return '\x00';
    if (code >= 64 && code <= 95) return String.fromCharCode(code - 64);
    return value;
  }

  interface TerminalKey { label: string; title: string; data?: string; cursor?: string; ctrl?: boolean }
  const keys: TerminalKey[] = [
    { label: 'Esc', title: 'Escape', data: '\x1b' },
    { label: 'Tab', title: 'Tab', data: '\t' },
    { label: 'Ctrl', title: 'Ctrl — hold for the next key', ctrl: true },
    { label: '↑', title: 'Up', cursor: 'A' },
    { label: '↓', title: 'Down', cursor: 'B' },
    { label: '←', title: 'Left', cursor: 'D' },
    { label: '→', title: 'Right', cursor: 'C' },
    { label: '^C', title: 'Ctrl+C — interrupt', data: '\x03' },
    { label: '^D', title: 'Ctrl+D — end of input', data: '\x04' },
    { label: '^Z', title: 'Ctrl+Z — suspend', data: '\x1a' },
    { label: 'Home', title: 'Home', cursor: 'H' },
    { label: 'End', title: 'End', cursor: 'F' },
    { label: 'PgUp', title: 'Page up', data: '\x1b[5~' },
    { label: 'PgDn', title: 'Page down', data: '\x1b[6~' },
    { label: '|', title: 'Pipe', data: '|' },
    { label: '~', title: 'Tilde', data: '~' },
    { label: '/', title: 'Slash', data: '/' },
    { label: '-', title: 'Hyphen', data: '-' },
  ];

  // Ctrl+C belongs to the shell, so the usual copy shortcut interrupts the
  // running command instead of copying, and Ctrl+Shift+C is not a browser
  // binding. Selecting with the mouse and pressing this is the explicit way out.
  export async function copySelection(): Promise<'copied' | 'empty' | 'failed'> {
    const active = term;
    const text = active?.getSelection() || '';
    if (!text) return 'empty';
    let copied = false;
    try {
      // A terminal is often reached over plain HTTP on a LAN, where the async
      // clipboard API is absent because the page is not a secure context.
      if (navigator.clipboard?.writeText) { await navigator.clipboard.writeText(text); copied = true; }
    } catch { /* blocked by permission or focus; the legacy path may still work */ }
    if (!copied) copied = legacyCopy(text);
    // Typing should continue where it left off, and xterm keeps the selection
    // visible so it is obvious what was taken.
    active?.focus();
    return copied ? 'copied' : 'failed';
  }

  function legacyCopy(text: string): boolean {
    const area = document.createElement('textarea');
    area.value = text;
    area.setAttribute('readonly', '');
    area.style.position = 'fixed';
    area.style.top = '0';
    area.style.opacity = '0';
    document.body.appendChild(area);
    area.select();
    try { return document.execCommand('copy'); }
    catch { return false; }
    finally { area.remove(); }
  }

  // Asking for control also carries this screen's size, so the shell reflows to
  // the device that is taking over instead of staying at the old one's shape.
  export function takeControl() {
    const active = term;
    if (!active) return;
    resend(JSON.stringify({ type: 'control', cols: Math.max(2, active.cols), rows: Math.max(2, active.rows) }));
  }

  function press(key: TerminalKey) {
    if (!control) return;
    if (key.ctrl) {
      ctrlArmed = !ctrlArmed;
      term?.focus();
      return;
    }
    // Editors and pagers switch the cursor keys to application mode, where the
    // same arrow is SS3 rather than CSI; sending the wrong one prints letters.
    const data = key.cursor ? `\x1b${term?.modes.applicationCursorKeysMode ? 'O' : '['}${key.cursor}` : key.data ?? '';
    send(applyCtrl(data));
    term?.focus();
  }

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
    void loadFonts(options.fontFamily, options.fontSize);
  });

  // A bundled web font arrives after xterm has already measured the cell, so
  // the grid would stay sized for the fallback until something else resized the
  // panel. Wait for the faces, then fit again with the real metrics.
  async function loadFonts(list: string, px: number) {
    if (!document.fonts?.load) return;
    try {
      await Promise.all([document.fonts.load(`${px}px ${list}`), document.fonts.load(`bold ${px}px ${list}`)]);
    } catch { /* an unavailable face still renders through the fallback */ }
    refit();
  }

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
    function frame(value: string) {
      if (ready && ws.readyState === WebSocket.OPEN) ws.send(value);
    }
    resend = frame;
    function write(value: string) {
      if (!ready || !control || ws.readyState !== WebSocket.OPEN) return;
      const data = new TextEncoder().encode(value);
      if (ws.bufferedAmount + data.length > 1024 * 1024) {
        failed = true;
        onstatus('error', 'Input is arriving faster than the connection can send it. Reconnect and paste a smaller block.');
        ws.close();
        return;
      }
      for (let offset = 0; offset < data.length; offset += 8192) ws.send(data.slice(offset, offset + 8192));
    }
    send = write;
    const input = instance.onData((value) => write(applyCtrl(value)));
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
        } else if (message.type === 'role') {
          // Not a connection state: the shell is fine, this screen is simply
          // watching, so the status line must not be turned into an error.
          control = !!message.control;
          ctrlArmed = false;
          onrole?.(control, Number(message.watchers) || 0);
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
      send = () => {};
      resend = () => {};
      ws.close();
      observer.disconnect();
      themeObserver.disconnect();
      input.dispose();
      instance.dispose();
    };
  });
</script>

<div class="flex h-full min-h-0 w-full flex-col" style:background={theme.background}>
  <div bind:this={container} class="min-h-0 w-full flex-1 overflow-hidden" aria-label="Interactive Linux terminal"></div>
  {#if keyBar}
    <!-- Sits inside the terminal frame so the fit addon reserves room for it and
    the host is told the smaller row count. -->
    <div class="flex shrink-0 gap-1 overflow-x-auto border-t px-2 py-1.5" style:border-color={theme.selectionBackground} role="group" aria-label="Terminal keys">
      {#each keys as key (key.label)}
        <button
          type="button"
          class="min-h-9 shrink-0 rounded-md border px-2.5 font-mono text-xs leading-none touch-manipulation focus-visible:outline-2 focus-visible:outline-offset-2 disabled:opacity-40"
          style:border-color={theme.selectionBackground}
          style:color={theme.foreground}
          style:background={key.ctrl && ctrlArmed ? theme.selectionBackground : 'transparent'}
          disabled={!control}
          title={key.title}
          aria-label={key.title}
          aria-pressed={key.ctrl ? ctrlArmed : undefined}
          onpointerdown={(e) => { e.preventDefault(); press(key); }}
          onclick={(e) => { if (e.detail === 0) press(key); }}
        >{key.label}</button>
      {/each}
    </div>
  {/if}
</div>
