<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import DocsCallout from './DocsCallout.svelte';
  import DocsEndpoint from './DocsEndpoint.svelte';
  import DocsParamTable from './DocsParamTable.svelte';
  import type { DocsParam } from './docs-types';
  import { curlChatExtensionsExample, curlResponsesExample } from './snippets';

  interface Props {
    baseUrl: string;
    model: string;
    fallbackModel: string;
  }

  let { baseUrl, model, fallbackModel }: Props = $props();

  const standard: DocsParam[] = [
    { name: 'stream', type: 'boolean', description: 'Server-sent events. Add stream_options.include_usage for a final usage chunk.' },
    { name: 'tools / tool_choice', type: 'array / string|object', description: 'Function calling. tool_choice is translated for every provider (auto, required, none, or a named function).' },
    { name: 'parallel_tool_calls', type: 'boolean', description: 'Maps to Anthropic’s disable_parallel_tool_use; forwarded elsewhere.' },
    { name: 'response_format', type: 'object', description: 'json_object or json_schema. Native on OpenAI, Gemini and Cohere; requested through the system prompt on Anthropic.' },
    { name: 'reasoning_effort', type: 'string', description: 'none, minimal, low, medium, high, xhigh or max — the levels a model supports are listed in /gateway/v1/model/info.' },
    { name: 'n', type: 'integer', description: 'Only 1 is supported; other values answer 400 before any provider is called.' },
    { name: 'seed, logprobs, user, metadata', type: '', description: 'Forwarded where the provider supports them and ignored otherwise.' },
  ];

  const extensions: DocsParam[] = [
    { name: 'at_fallbacks', type: 'string[]', description: 'Ordered provider/model ids tried when the primary fails with 429, 529, 5xx or a timeout. See Routing & fallbacks.' },
    { name: 'timeout_ms', type: 'integer', description: 'Deadline for the whole call, across every fallback attempt.' },
    { name: 'extra_body', type: 'object', description: 'Merged into the upstream request after AT’s own mapping, for provider-native fields (cache_control, safetySettings, …).' },
    { name: 'mock_response', type: 'string', description: 'Return this text immediately without calling a provider. Works for streaming too. Handy in tests.' },
    { name: 'Idempotency-Key', type: 'header', description: 'Replays the stored response for the same key and token for 5 minutes. 5xx responses are not stored.' },
  ];

  const responseHeaders: DocsParam[] = [
    { name: 'x-at-model-used', description: 'The provider/model that actually answered, after fallbacks.' },
    { name: 'x-at-routing-profile', description: 'The routing profile a bare model name resolved to.' },
    { name: 'x-at-trace-id', description: 'Trace of this call, for Traces and /gateway/v1/scores.' },
    { name: 'x-at-idempotent-replay', description: 'true when the body is a replay of an earlier Idempotency-Key request.' },
    { name: 'x-at-mock-response', description: 'true when mock_response produced the answer.' },
  ];
</script>

<div class="space-y-1.5">
  <DocsEndpoint method="POST" path="/gateway/v1/chat/completions" {baseUrl} />
  <DocsEndpoint method="POST" path="/gateway/v1/responses" {baseUrl} />
</div>

<div class="docs-prose">
  <p>
    Both endpoints accept the OpenAI request shape unchanged, for every provider type. Tool calls,
    finish reasons, refusals and usage (including <code>reasoning_tokens</code>) come back in
    OpenAI shape regardless of which provider answered.
  </p>
  <h3>Standard fields worth knowing</h3>
</div>
<DocsParamTable params={standard} />

<div class="docs-prose">
  <h3>AT extensions</h3>
  <p>All optional. Without them the gateway behaves exactly like the upstream OpenAI API.</p>
</div>
<DocsParamTable params={extensions} />

<DocsCodeBlock
  code={curlChatExtensionsExample(baseUrl, model, fallbackModel)}
  lang="bash"
  label="Streaming with a fallback"
  copyLabel="Copy streaming example"
/>

<div class="docs-prose"><h3>Response headers</h3></div>
<DocsParamTable params={responseHeaders} nameLabel="Header" showType={false} />

<div class="docs-prose">
  <h3>Responses API</h3>
  <p>
    <code>/responses</code> supports <code>input</code> (a string or an item array),
    <code>instructions</code>, function <code>tools</code>, <code>reasoning.effort</code> and
    <code>text.format</code>, with the standard streaming events. It keeps no server-side state, so
    send the whole conversation on each call.
  </p>
</div>
<DocsCodeBlock code={curlResponsesExample(baseUrl, model)} lang="bash" label="curl" copyLabel="Copy responses example" />

<DocsCallout>
  <p>
    Errors from a provider are returned as real HTTP errors (429, 5xx) in the OpenAI error
    envelope — never as a 200 with error text in the content. See <a href="#/docs?section=errors">Errors</a>.
  </p>
</DocsCallout>
