<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import DocsEndpoint from './DocsEndpoint.svelte';
  import DocsParamTable from './DocsParamTable.svelte';
  import DocsCallout from './DocsCallout.svelte';
  import type { DocsParam } from './docs-types';
  import { curlScoreExample, curlTraceLabelsExample } from './snippets';

  interface Props {
    baseUrl: string;
    model: string;
  }

  let { baseUrl, model }: Props = $props();

  const headers: DocsParam[] = [
    { name: 'x-at-trace-id', description: 'Group several calls into one trace. Generated when omitted and returned on the response.' },
    { name: 'x-at-session-id', description: 'Group traces into a session (a conversation). X-Session-Id, x-opencode-session and x-claude-code-session-id are recognised as well.' },
    { name: 'x-at-trace-name', description: 'A readable name for the trace.' },
    { name: 'x-at-tags', description: 'Comma-separated tags to filter by.' },
    { name: 'x-at-environment / x-at-release', description: 'Deployment labels, e.g. production and v2.3.1.' },
    { name: 'x-at-user', description: 'Your own end-user id. Display only — it never grants or changes access.' },
  ];
</script>

<div class="docs-prose">
  <p>
    Every gateway call is recorded as a trace you can inspect on the <a href="#/traces">Traces</a>
    page — request, response, tokens, cost and latency. All labels below are optional; they only
    help you find and filter traces.
  </p>
</div>

<DocsParamTable params={headers} nameLabel="Request header" showType={false} />

<DocsCodeBlock
  code={curlTraceLabelsExample(baseUrl, model)}
  lang="bash"
  label="Labelled request"
  copyLabel="Copy labelled request"
/>

<DocsCallout title="Sessions without a trace id">
  <p>
    When a request carries a session id but no trace id, every model call of one user turn — tool
    calls included — lands in the same trace, and the next user message starts a new one. Coding
    agents get readable traces without any extra work.
  </p>
</DocsCallout>

<div class="docs-prose">
  <h3>Scores</h3>
  <p>
    Attach a quality signal to a trace — numeric (<code>value</code>), boolean
    (<code>bool_value</code>) or categorical (<code>string_value</code>), optionally on one
    <code>observation_id</code>. A token can only score traces it produced itself.
  </p>
</div>

<DocsEndpoint method="POST" path="/gateway/v1/scores" {baseUrl} />
<DocsCodeBlock code={curlScoreExample(baseUrl)} lang="bash" label="curl" copyLabel="Copy score request" />
