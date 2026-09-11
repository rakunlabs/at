<script lang="ts">
  interface Props {
    baseUrl: string;
  }

  let { baseUrl }: Props = $props();

  const endpoints = [
    {
      method: 'POST',
      path: '/gateway/v1/chat/completions',
      description: 'Send chat messages. Compatible with the OpenAI Chat Completions API.',
      tone: 'text-green-800 bg-green-50 border-green-300 dark:text-green-300 dark:bg-green-900/20 dark:border-green-800',
    },
    {
      method: 'GET',
      path: '/gateway/v1/models',
      description: 'List all available models.',
      tone: 'text-blue-800 bg-blue-50 border-blue-300 dark:text-blue-300 dark:bg-blue-900/20 dark:border-blue-800',
    },
  ];
</script>

<ul class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface">
  {#each endpoints as ep (ep.path)}
    <li class="border-b border-gray-200 p-3 last:border-b-0 dark:border-dark-border">
      <div class="flex flex-wrap items-center gap-2">
        <span class={['shrink-0 border px-2 py-0.5 font-mono text-[11px] font-semibold', ep.tone]}>
          {ep.method}
        </span>
        <code class="break-all font-mono text-[12px] text-gray-800 dark:text-dark-text">
          {baseUrl}{ep.path}
        </code>
      </div>
      <p class="mt-1.5 text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
        {ep.description}
      </p>
    </li>
  {/each}
</ul>
