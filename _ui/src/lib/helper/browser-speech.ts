interface SpeechResult {
  isFinal: boolean;
  [index: number]: { transcript: string };
}

interface Recognition {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  onresult: ((event: { resultIndex: number; results: ArrayLike<SpeechResult> }) => void) | null;
  onerror: ((event: { error: string }) => void) | null;
  onend: (() => void) | null;
  start(): void;
  stop(): void;
  abort(): void;
}

interface SpeechWindow {
  SpeechRecognition?: new () => Recognition;
  webkitSpeechRecognition?: new () => Recognition;
}

function recognitionConstructor() {
  if (typeof window === 'undefined') return undefined;
  const browser = window as unknown as SpeechWindow;
  return browser.SpeechRecognition || browser.webkitSpeechRecognition;
}

export function browserSpeechSupported(): boolean {
  return !!recognitionConstructor();
}

export interface BrowserSpeechSession { stop(): void; cancel(): void }
interface Options {
  language: string;
  onText(text: string): void;
  onError(message: string): void;
  onEnd(): void;
}

/** Only finalized segments enter the draft; cancellation discards late results. */
export function startBrowserSpeech(options: Options): BrowserSpeechSession {
  const Constructor = recognitionConstructor();
  if (!Constructor) throw new Error('Browser dictation is unavailable. Choose an API method or use your keyboard’s microphone.');
  const recognition = new Constructor();
  recognition.lang = options.language;
  recognition.continuous = true;
  recognition.interimResults = false;
  const delivered = new Set<number>();
  let ended = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const finish = (abort = false) => {
    if (ended) return;
    ended = true;
    clearTimeout(timer);
    recognition.onresult = recognition.onerror = recognition.onend = null;
    if (abort) { try { recognition.abort(); } catch { /* already stopped */ } }
    options.onEnd();
  };
  recognition.onresult = event => {
    if (ended) return;
    for (let i = event.resultIndex; i < event.results.length; i++) {
      const result = event.results[i];
      if (!result.isFinal || delivered.has(i)) continue;
      delivered.add(i);
      const text = result[0]?.transcript.trim();
      if (text) options.onText(text);
    }
  };
  recognition.onerror = event => {
    if (ended) return;
    const messages: Record<string, string> = {
      'not-allowed': 'Microphone or speech permission was denied. Allow access in browser settings and try again.',
      'service-not-allowed': 'This browser does not allow its speech service. Choose an API method or keyboard dictation.',
      'audio-capture': 'No microphone is available. Check your device and microphone permissions.',
      'network': 'The browser speech service could not connect. Check your connection or choose an API method.',
      'no-speech': 'No speech was detected. Try again and speak after the microphone starts.',
      'language-not-supported': 'The browser speech service does not support this language. Choose another language or method.',
    };
    if (event.error !== 'aborted') options.onError(messages[event.error] || 'Browser dictation failed. Try again or choose an API method.');
    finish(true);
  };
  recognition.onend = () => finish();
  try { recognition.start(); } catch (error) { finish(true); throw error; }
  return {
    stop() {
      if (ended || timer) return;
      // stop() lets the engine deliver its last final result before onend.
      timer = setTimeout(() => {
        if (!ended) options.onError('The browser speech service did not finish. You can retry dictation.');
        finish(true);
      }, 5000);
      try { recognition.stop(); } catch { finish(true); }
    },
    cancel() { finish(true); },
  };
}
