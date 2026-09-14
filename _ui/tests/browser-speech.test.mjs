import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { test } from 'node:test';
import ts from 'typescript';

const source = await readFile(new URL('../src/lib/helper/browser-speech.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText;
const { browserSpeechSupported, startBrowserSpeech } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);

function setup(prefixed = false) {
  let engine;
  class Recognition {
    constructor() { engine = this; }
    start() { this.started = true; }
    stop() { this.stopped = true; }
    abort() { this.aborted = true; }
  }
  globalThis.window = prefixed ? { webkitSpeechRecognition: Recognition } : { SpeechRecognition: Recognition };
  const texts = [], errors = [];
  let ends = 0;
  const session = startBrowserSpeech({ language: 'tr-TR', onText: text => texts.push(text), onError: error => errors.push(error), onEnd: () => ends++ });
  return { engine, session, texts, errors, get ends() { return ends; } };
}
const result = (text, final = true) => ({ isFinal: final, 0: { transcript: text } });

test('standard and prefixed browser speech preserve language and deliver only distinct final segments', () => {
  for (const prefixed of [false, true]) {
    const h = setup(prefixed);
    assert.equal(browserSpeechSupported(), true);
    assert.equal(h.engine.lang, 'tr-TR');
    assert.equal(h.engine.continuous, true);
    h.engine.onresult({ resultIndex: 0, results: [result('partial', false)] });
    h.engine.onresult({ resultIndex: 0, results: [result(' Merhaba dünya ')] });
    h.engine.onresult({ resultIndex: 0, results: [result('Merhaba dünya'), result('İkinci cümle.')] });
    assert.deepEqual(h.texts, ['Merhaba dünya', 'İkinci cümle.']);
    h.engine.onend();
    assert.equal(h.ends, 1);
    assert.equal(h.engine.onresult, null);
  }
});

test('stop waits for the final result but cancel rejects callbacks already queued by the browser', () => {
  const h = setup();
  h.session.stop();
  assert.equal(h.engine.stopped, true);
  assert.equal(h.ends, 0);
  h.engine.onresult({ resultIndex: 0, results: [result('last words')] });
  h.engine.onend();
  assert.deepEqual(h.texts, ['last words']);
  assert.equal(h.ends, 1);

  const cancelled = setup();
  const queued = cancelled.engine.onresult;
  const queuedEnd = cancelled.engine.onend;
  cancelled.session.cancel();
  queued({ resultIndex: 0, results: [result('do not insert in the next conversation')] });
  queuedEnd();
  cancelled.session.cancel();
  assert.deepEqual(cancelled.texts, []);
  assert.equal(cancelled.engine.aborted, true);
  assert.equal(cancelled.ends, 1);
});

test('permission, network and unsupported-language errors finish the session with actionable feedback', () => {
  for (const error of ['not-allowed', 'service-not-allowed', 'network', 'language-not-supported', 'no-speech']) {
    const h = setup();
    h.engine.onerror({ error });
    assert.equal(h.errors.length, 1);
    assert.equal(h.ends, 1);
    assert.equal(h.engine.aborted, true);
    assert.equal(h.engine.onresult, null);
  }
});

test('missing browser support is reported instead of switching to a paid API', () => {
  globalThis.window = {};
  assert.equal(browserSpeechSupported(), false);
  assert.throws(() => startBrowserSpeech({}), /unavailable/);
});
