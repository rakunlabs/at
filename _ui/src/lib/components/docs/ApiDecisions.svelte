<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';
  import DocsEndpoint from './DocsEndpoint.svelte';
  import DocsParamTable from './DocsParamTable.svelte';
  import DocsCallout from './DocsCallout.svelte';
  import type { DocsParam } from './docs-types';
  import { curlDecisionExample } from './snippets';

  interface Props {
    baseUrl: string;
  }

  let { baseUrl }: Props = $props();

  const types: DocsParam[] = [
    { name: 'choice', description: 'Pick one key from criteria. Requires a criteria object of key → description.' },
    { name: 'score', description: 'An ordinal level, for ratings and severities.' },
    { name: 'noul', description: 'The probability that the statement in instructions holds for the state.' },
  ];
</script>

<DocsEndpoint method="POST" path="/gateway/v1/decisions" {baseUrl} />

<div class="docs-prose">
  <p>
    A <strong>decision model</strong> (Laya, TypeSafe Jev) answers typed questions about a piece of
    text in a single forward pass, with calibrated probabilities, and never generates text. It is
    much cheaper than asking a chat model to classify. AT attaches an external service speaking
    <code>POST /v1/systemone</code> as a provider of type <code>systemone</code>.
  </p>
  <p>
    The body is <code>{'{model, state, questions}'}</code> plus any control the upstream understands
    (<code>min_confidence</code>, <code>lang</code>, <code>max_len</code>, …), which is forwarded
    unchanged. Use <code>&lt;provider&gt;/auto</code> to let the service choose a checkpoint. Up to
    64 questions per call.
  </p>
</div>

<DocsParamTable params={types} nameLabel="Question type" showType={false} />

<DocsCodeBlock code={curlDecisionExample(baseUrl)} lang="bash" label="curl" copyLabel="Copy decision request" />

<DocsCallout tone="warning" title="Calibrate before trusting the numbers">
  <p>
    Shipped checkpoints are weak zero-shot on domain decisions and over-confident until
    temperature-fitted. Fit thresholds on your own labelled data. Decision models are not listed in
    <code>/gateway/v1/models</code>, because chat clients cannot use them.
  </p>
</DocsCallout>
