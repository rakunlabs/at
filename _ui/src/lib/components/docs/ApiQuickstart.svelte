<script lang="ts">
  import DocsCodeTabs from './DocsCodeTabs.svelte';
  import DocsCallout from './DocsCallout.svelte';
  import { codeExampleTabs, codeExampleFor } from './snippets';

  interface Props {
    baseUrl: string;
    model: string;
    /** Lifted so the chosen language survives navigating away and back. */
    activeTab?: string;
  }

  let { baseUrl, model, activeTab = $bindable('python') }: Props = $props();

  const tabs = $derived(
    codeExampleTabs.map((t) => ({ ...t, code: codeExampleFor(t.id, model, baseUrl) })),
  );

  const step =
    'flex size-5 shrink-0 items-center justify-center border border-dark-border-subtle text-[11px] text-dark-text-secondary';
</script>

<ol class="space-y-6">
  <li class="flex gap-3">
    <span class={step}>1</span>
    <div class="docs-prose min-w-0 flex-1">
      <h3 class="!pt-0">Create an API token</h3>
      <p>
        Open <a href="#/settings/tokens">Settings → API tokens</a> and create a token. The secret
        starts with <code>at_</code> and is shown once — store it like a password. A
        <strong>personal</strong> token can reach your own providers too; a
        <strong>workspace</strong> token is shared by the workspace.
      </p>
    </div>
  </li>

  <li class="flex gap-3">
    <span class={step}>2</span>
    <div class="docs-prose min-w-0 flex-1">
      <h3 class="!pt-0">Pick a model</h3>
      <p>
        These examples use <code>{model}</code>. Any id from
        <a href="#/docs?section=available-models">Models</a> works the same way.
      </p>
    </div>
  </li>

  <li class="flex gap-3">
    <span class={step}>3</span>
    <div class="min-w-0 flex-1 space-y-3">
      <div class="docs-prose">
        <h3 class="!pt-0">Point an OpenAI client at the gateway</h3>
        <p>Change only the base URL and the API key. Everything else is the standard SDK.</p>
      </div>
      <DocsCodeTabs {tabs} bind:active={activeTab} name="quickstart" />
    </div>
  </li>
</ol>

<DocsCallout title="Using an Anthropic client?">
  <p>
    Claude Code and the Anthropic SDKs talk to <code>{baseUrl}/gateway</code> natively — see
    <a href="#/docs?section=anthropic-messages">Anthropic Messages</a>.
  </p>
</DocsCallout>
