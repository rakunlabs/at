<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import DocsEndpoint from './DocsEndpoint.svelte';
  import DocsCallout from './DocsCallout.svelte';
  import {
    curlImageExample,
    curlModerationExample,
    curlRerankExample,
    curlSpeechExample,
    curlTranscriptionExample,
  } from './snippets';

  interface Props {
    baseUrl: string;
  }

  let { baseUrl }: Props = $props();
</script>

<div class="docs-prose">
  <p>
    These endpoints follow the OpenAI (and, for rerank, Cohere) request shapes. The
    <code>model</code> is a <code>provider/model</code> id like everywhere else; the provider must
    support the operation, otherwise the request fails with a clear error.
  </p>
</div>

<section class="space-y-3">
  <div class="docs-prose"><h3>Images</h3></div>
  <DocsEndpoint method="POST" path="/gateway/v1/images/generations" {baseUrl} />
  <div class="docs-prose">
    <p>
      Supported by OpenAI providers with an API key (<code>gpt-image-*</code>,
      <code>dall-e-*</code>), OpenAI providers using ChatGPT sign-in (billed to the subscription)
      and MiniMax. Fields: <code>prompt</code>, <code>size</code>, <code>quality</code>,
      <code>background</code>, <code>n</code>.
    </p>
  </div>
  <DocsCodeBlock code={curlImageExample(baseUrl)} lang="bash" label="curl" copyLabel="Copy image request" />
</section>

<section class="space-y-3">
  <div class="docs-prose"><h3>Speech</h3></div>
  <DocsEndpoint method="POST" path="/gateway/v1/audio/speech" {baseUrl} />
  <div class="docs-prose">
    <p>
      Text to speech. The response is the raw audio file. Fields: <code>input</code>,
      <code>voice</code>, <code>response_format</code> (mp3, opus, aac, flac), <code>speed</code>.
    </p>
  </div>
  <DocsCodeBlock code={curlSpeechExample(baseUrl)} lang="bash" label="curl" copyLabel="Copy speech request" />
</section>

<section class="space-y-3">
  <div class="docs-prose"><h3>Transcription</h3></div>
  <DocsEndpoint method="POST" path="/gateway/v1/audio/transcriptions" {baseUrl} />
  <div class="docs-prose">
    <p>
      Whisper-style multipart upload with <code>file</code>, <code>model</code> and optional
      <code>language</code>, <code>prompt</code> and <code>response_format</code> (json, text, srt,
      vtt, verbose_json).
    </p>
  </div>
  <DocsCodeBlock
    code={curlTranscriptionExample(baseUrl)}
    lang="bash"
    label="curl"
    copyLabel="Copy transcription request"
  />
</section>

<section class="space-y-3">
  <div class="docs-prose"><h3>Moderation</h3></div>
  <DocsEndpoint method="POST" path="/gateway/v1/moderations" {baseUrl} />
  <DocsCodeBlock code={curlModerationExample(baseUrl)} lang="bash" label="curl" copyLabel="Copy moderation request" />
</section>

<section class="space-y-3">
  <div class="docs-prose"><h3>Rerank</h3></div>
  <DocsEndpoint method="POST" path="/gateway/v1/rerank" {baseUrl} />
  <div class="docs-prose">
    <p>
      Cohere shape: <code>query</code>, <code>documents</code>, optional <code>top_n</code> and
      <code>return_documents</code>. Results are ordered by relevance and carry each document’s
      original <code>index</code>.
    </p>
  </div>
  <DocsCodeBlock code={curlRerankExample(baseUrl)} lang="bash" label="curl" copyLabel="Copy rerank request" />
</section>

<section class="space-y-3">
  <div class="docs-prose"><h3>Downloading generated media</h3></div>
  <DocsEndpoint method="GET" path="/gateway/v1/media/{'{id}'}" {baseUrl} />
  <DocsCallout>
    <p>
      Media created through the gateway — for example by the <code>generate_image</code> MCP tool —
      can be downloaded by the same token, or with the <code>download_url</code> returned by the
      tool, which carries its own key and expires after 24 hours by default.
    </p>
  </DocsCallout>
</section>
