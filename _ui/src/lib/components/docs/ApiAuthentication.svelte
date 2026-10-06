<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';

  const header = 'Authorization: Bearer at_your_token_here';
</script>

<p class="text-sm leading-relaxed text-dark-text-secondary">
  Include your API token in the
  <code
    class="font-mono bg-dark-elevated px-1.5 py-0.5 text-[12px] text-dark-text-secondary"
    >Authorization</code
  > header of every request:
</p>

<DocsCodeBlock code={header} lang="http" label="request header" copyLabel="Copy authorization header" />

<p class="text-sm leading-relaxed text-dark-text-secondary">
  Generate tokens from the
  <a
    href="#/settings/tokens"
    class="underline underline-offset-2 hover:no-underline focus-visible:outline-2 focus-visible:outline-offset-2 text-accent-text focus-visible:outline-accent"
    >Tokens</a
  > page. Tokens can optionally be scoped to specific providers or models.
</p>
