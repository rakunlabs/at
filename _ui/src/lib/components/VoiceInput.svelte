<script lang="ts">
  import { onDestroy, untrack } from 'svelte';
  import axios from 'axios';
  import { Mic, MicOff, Loader2, Settings2, X } from 'lucide-svelte';
  import { addToast } from '@/lib/store/toast.svelte';
  import { browserSpeechSupported, startBrowserSpeech, type BrowserSpeechSession } from '@/lib/helper/browser-speech';

  interface Props {
    disabled?: boolean;
    contextKey: number;
    recording?: boolean;
    transcribing?: boolean;
    ontext: (text: string) => void;
  }
  let { disabled = false, contextKey, recording = $bindable(false), transcribing = $bindable(false), ontext }: Props = $props();
  function preference(key: string, fallback: string, values: string[]) {
    try { const value = localStorage.getItem(key) || ''; return values.includes(value) ? value : fallback; }
    catch { return fallback; }
  }
  function save(key: string, value: string) { try { localStorage.setItem(key, value); } catch { /* usable without persistence */ } }
  const methods = [
    { value: 'openai', label: 'OpenAI API (cloud)' },
    { value: 'browser', label: 'Browser dictation' },
    { value: 'local', label: 'Local Whisper (server)' },
    { value: 'faster-whisper', label: 'Faster-Whisper (server)' },
  ];
  const languages = [
    { value: 'auto', label: 'Browser language' }, { value: 'tr-TR', label: 'Türkçe' },
    { value: 'en-US', label: 'English (US)' }, { value: 'en-GB', label: 'English (UK)' },
    { value: 'de-DE', label: 'Deutsch' }, { value: 'fr-FR', label: 'Français' },
    { value: 'es-ES', label: 'Español' }, { value: 'pt-BR', label: 'Português' },
    { value: 'ar-SA', label: 'العربية' }, { value: 'ja-JP', label: '日本語' },
  ];
  let method = $state(preference('at-voice-method', 'openai', methods.map(m => m.value)));
  let model = $state(preference('at-voice-model', 'tiny', ['tiny', 'base', 'small', 'medium']));
  let language = $state(preference('at-voice-language', 'auto', languages.map(l => l.value)));
  const browserSupported = browserSpeechSupported();
  let starting = $state(false);
  let duration = $state(0);
  let timer: ReturnType<typeof setInterval> | undefined;
  let recorder: MediaRecorder | null = null;
  let speech: BrowserSpeechSession | null = null;
  let request: AbortController | null = null;
  let version = 0;
  let settings = $state<HTMLDivElement>();
  let settingsOpen = $state(false);
  let settingsLeft = $state(12);
  let settingsBottom = $state(64);
  const settingsId = $props.id();
  const control = 'inline-flex h-11 min-w-11 sm:h-10 sm:min-w-10 shrink-0 items-center justify-center gap-1 rounded-md border border-gray-200 dark:border-dark-border text-gray-600 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:opacity-40 disabled:cursor-not-allowed';
  const label = $derived(method === 'browser' ? 'Browser' : method === 'openai' ? 'API' : model);

  function clearTimer() { clearInterval(timer); timer = undefined; duration = 0; }
  function cancel() {
    ++version;
    speech?.cancel(); speech = null;
    request?.abort(); request = null;
    if (recorder) {
      recorder.onstop = recorder.ondataavailable = recorder.onerror = null;
      if (recorder.state !== 'inactive') recorder.stop();
      recorder.stream.getTracks().forEach(track => track.stop());
      recorder = null;
    }
    starting = recording = transcribing = false;
    clearTimer();
    settings?.hidePopover();
  }
  $effect(() => { void contextKey; untrack(cancel); });
  onDestroy(cancel);

  function startTimer() { duration = 0; timer = setInterval(() => { duration++; }, 1000); }
  async function start() {
    if (disabled || recording || transcribing || starting) return;
    const run = ++version;
    const key = contextKey;
    const current = () => run === version && key === contextKey;
    const selectedMethod = method;
    const selectedModel = model;
    settings?.hidePopover();
    recording = true;
    if (selectedMethod === 'browser') {
      try {
        startTimer();
        speech = startBrowserSpeech({
          language: language === 'auto' ? navigator.language || 'en-US' : language,
          onText: text => { if (current()) ontext(text); },
          onError: message => { if (current()) addToast(message, 'alert'); },
          onEnd: () => { if (current()) { recording = transcribing = false; speech = null; clearTimer(); } },
        });
      } catch (e) { if (current()) { cancel(); addToast(e instanceof Error ? e.message : 'Could not start browser dictation.', 'alert'); } }
      return;
    }
    starting = true;
    let stream: MediaStream | null = null;
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      if (!current()) { stream.getTracks().forEach(track => track.stop()); return; }
      const mimeType = ['audio/webm', 'audio/mp4'].find(type => MediaRecorder.isTypeSupported(type));
      const activeRecorder = mimeType ? new MediaRecorder(stream, { mimeType }) : new MediaRecorder(stream);
      recorder = activeRecorder;
      const chunks: BlobPart[] = [];
      activeRecorder.ondataavailable = event => { if (event.data.size > 0) chunks.push(event.data); };
      activeRecorder.onerror = () => { if (current()) { cancel(); addToast('Voice recording failed. Check the microphone and try again.', 'alert'); } };
      activeRecorder.onstop = async () => {
        activeRecorder.stream.getTracks().forEach(track => track.stop());
        if (!current()) return;
        recorder = null;
        recording = false;
        clearTimer();
        transcribing = true;
        const controller = new AbortController();
        request = controller;
        try {
          const type = activeRecorder.mimeType || 'audio/webm';
          const form = new FormData();
          form.append('file', new Blob(chunks, { type }), type.includes('mp4') ? 'voice.m4a' : 'voice.webm');
          const params = selectedMethod === 'openai' ? '' : `?${new URLSearchParams({ method: selectedMethod, model: selectedModel })}`;
          const response = await axios.post(`api/v1/audio/transcribe${params}`, form, { signal: controller.signal });
          if (!current()) return;
          if (response.data?.text?.trim()) ontext(response.data.text.trim());
          else addToast('No speech was recognized. Try recording again.', 'warn');
        } catch (e: any) {
          if (current() && !controller.signal.aborted) addToast(e?.response?.data?.message || 'Transcription failed. Check the configured speech provider and try again.', 'alert');
        } finally { if (current()) { transcribing = false; request = null; } }
      };
      activeRecorder.start(1000);
      starting = false;
      startTimer();
    } catch {
      stream?.getTracks().forEach(track => track.stop());
      if (current()) { cancel(); addToast('Could not access the microphone. Check browser permissions and use HTTPS.', 'alert'); }
    }
  }
  function stop() {
    if (starting) { cancel(); return; }
    if (speech) { recording = false; transcribing = true; clearTimer(); speech.stop(); }
    else if (recorder?.state === 'recording') { recording = false; transcribing = true; recorder.stop(); clearTimer(); }
  }
  function toggleSettings(event: MouseEvent) {
    const rect = (event.currentTarget as HTMLElement).getBoundingClientRect();
    const width = Math.min(288, window.innerWidth - 24);
    settingsLeft = Math.max(12, Math.min(rect.right - width, window.innerWidth - width - 12));
    settingsBottom = Math.max(12, window.innerHeight - rect.top + 8);
    settings?.togglePopover();
  }
</script>

<div class="flex shrink-0 items-center gap-1">
  {#if recording}
    <button onclick={stop} class={`${control} px-2 !text-red-700 dark:!text-red-300`} aria-label={starting ? 'Cancel microphone request' : 'Stop voice input'} title="Stop voice input">
      <MicOff size={18} /><span class="text-xs tabular-nums">{starting ? '…' : `${Math.floor(duration / 60)}:${String(duration % 60).padStart(2, '0')}`}</span>
    </button>
    <span role="status" class="sr-only">{starting ? 'Waiting for microphone permission' : method === 'browser' ? 'Listening with browser dictation' : 'Recording voice'}</span>
  {:else if transcribing}
    <button onclick={cancel} class={control} aria-label="Cancel transcription" title="Cancel transcription"><Loader2 size={18} class="animate-spin motion-reduce:animate-none" /></button>
    <span role="status" class="sr-only">Transcribing voice</span>
  {:else}
    <button onclick={start} disabled={disabled || (method === 'browser' && !browserSupported)} class={control} aria-label={`Start voice input (${label})`} title={method === 'browser' && !browserSupported ? 'Browser dictation unavailable — choose another voice method' : `Voice input (${label})`}><Mic size={18} /></button>
  {/if}
  <button onclick={toggleSettings} class={control} disabled={recording || transcribing || disabled} aria-label="Voice settings" aria-expanded={settingsOpen} aria-controls={settingsId} title={`Voice settings (${label})`}><Settings2 size={16} /></button>
</div>

<div bind:this={settings} id={settingsId} popover="auto" role="dialog" aria-label="Voice input settings" ontoggle={event => { settingsOpen = event.newState === 'open'; }}
  style:left={`${settingsLeft}px`} style:bottom={`${settingsBottom}px`} style:max-height={`calc(100dvh - ${settingsBottom + 12}px)`}
  class="fixed top-auto right-auto m-0 w-72 max-w-[calc(100vw-1.5rem)] overflow-y-auto rounded-lg border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3 text-gray-900 dark:text-dark-text shadow-lg">
  <div class="mb-2 flex items-center justify-between gap-2"><h2 class="text-sm font-semibold">Voice input</h2><button onclick={() => settings?.hidePopover()} class="inline-flex size-11 sm:size-8 items-center justify-center rounded hover:bg-gray-100 dark:hover:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent" aria-label="Close voice settings"><X size={16} /></button></div>
  <label class="block text-sm">Transcription method
    <select value={method} onchange={event => { method = event.currentTarget.value; save('at-voice-method', method); }} class="mt-1 h-11 w-full rounded border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-elevated px-2 text-base sm:text-sm">
      {#each methods as option}<option value={option.value} disabled={option.value === 'browser' && !browserSupported}>{option.label}{option.value === 'browser' && !browserSupported ? ' — unavailable' : ''}</option>{/each}
    </select>
  </label>
  {#if method === 'browser'}
    <label class="mt-3 block text-sm">Dictation language
      <select value={language} onchange={event => { language = event.currentTarget.value; save('at-voice-language', language); }} class="mt-1 h-11 w-full rounded border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-elevated px-2 text-base sm:text-sm">{#each languages as option}<option value={option.value}>{option.label}</option>{/each}</select>
    </label>
    <p class="mt-3 text-xs leading-5 text-gray-600 dark:text-dark-text-secondary">Uses your browser’s speech service, not AT’s transcription API. The browser may use an online service; offline processing is not guaranteed.</p>
  {:else if method !== 'openai'}
    <label class="mt-3 block text-sm">Whisper model
      <select value={model} onchange={event => { model = event.currentTarget.value; save('at-voice-model', model); }} class="mt-1 h-11 w-full rounded border border-gray-300 dark:border-dark-border bg-white dark:bg-dark-elevated px-2 text-base sm:text-sm">{#each ['tiny', 'base', 'small', 'medium'] as value}<option {value}>{value}</option>{/each}</select>
    </label>
  {/if}
  {#if !browserSupported}<p class="mt-3 text-xs leading-5 text-gray-600 dark:text-dark-text-secondary">Browser dictation is unavailable here. Use an API method or your keyboard’s microphone.</p>{/if}
  <p class="mt-3 text-xs leading-5 text-gray-600 dark:text-dark-text-secondary">You can also tap the microphone on your phone’s keyboard or use your system’s dictation shortcut while the message field is focused.</p>
</div>
