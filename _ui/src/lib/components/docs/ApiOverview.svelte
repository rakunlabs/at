<script lang="ts">
  interface Props {
    baseUrl: string;
  }

  let { baseUrl }: Props = $props();
</script>

<p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
  This gateway provides an OpenAI-compatible API. You can use any OpenAI SDK or HTTP client to
  interact with your configured LLM providers. Models are addressed with the format
  <code
    class="font-mono bg-gray-100 dark:bg-dark-elevated px-1.5 py-0.5 text-[12px] text-gray-800 dark:text-dark-text-secondary"
    >provider_key/model_name</code
  >.
</p>

<dl class="grid gap-px border border-gray-200 dark:border-dark-border bg-gray-200 dark:bg-dark-border sm:grid-cols-2">
  <div class="bg-white dark:bg-dark-surface p-3">
    <dt class="text-[11px] font-medium uppercase tracking-wider text-gray-600 dark:text-dark-text-secondary">
      Base URL
    </dt>
    <dd class="mt-1 break-all font-mono text-[12px] text-gray-800 dark:text-dark-text">
      {baseUrl}/gateway/v1
    </dd>
  </div>
  <div class="bg-white dark:bg-dark-surface p-3">
    <dt class="text-[11px] font-medium uppercase tracking-wider text-gray-600 dark:text-dark-text-secondary">
      Auth
    </dt>
    <dd class="mt-1 break-all font-mono text-[12px] text-gray-800 dark:text-dark-text">
      Authorization: Bearer at_…
    </dd>
  </div>
</dl>

<p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
  Start with <a
    href="#/docs?section=code-examples"
    class="text-gray-900 underline underline-offset-2 hover:no-underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 dark:text-accent-text dark:focus-visible:outline-accent"
    >Code examples</a
  >, then create a token on the
  <a
    href="#/settings/tokens"
    class="text-gray-900 underline underline-offset-2 hover:no-underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-gray-900 dark:text-accent-text dark:focus-visible:outline-accent"
    >Tokens</a
  > page.
</p>
