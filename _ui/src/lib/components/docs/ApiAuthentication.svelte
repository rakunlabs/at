<script lang="ts">
  import DocsCodeBlock from './DocsCodeBlock.svelte';

  const header = 'Authorization: Bearer at_your_token_here';
</script>

<p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
  Include your API token in the
  <code
    class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
    >Authorization</code
  > header of every request:
</p>

<DocsCodeBlock code={header} lang="http" label="request header" copyLabel="Copy authorization header" />

<p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
  Generate tokens from the
  <a
    href="#/settings/tokens"
    class="text-gray-900 underline underline-offset-2 hover:no-underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 dark:text-accent-text dark:focus-visible:outline-accent"
    >Tokens</a
  > page. Tokens can optionally be scoped to specific providers or models.
</p>
