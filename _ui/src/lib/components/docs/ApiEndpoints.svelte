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
      tone: 'text-green-300 bg-green-900/20 border-green-800',
    },
    {
      method: 'POST',
      path: '/gateway/v1/decisions',
      description: 'System 1 typed decisions (choice / score / yes-no) from a systemone provider such as Laya or TypeSafe Jev. Body: { model, state, questions }.',
      tone: 'text-green-300 bg-green-900/20 border-green-800',
    },
    {
      method: 'GET',
      path: '/gateway/v1/models',
      description: 'List all available models.',
      tone: 'text-blue-300 bg-blue-900/20 border-blue-800',
    },
  ];
</script>

<ul class="border border-dark-border bg-dark-surface">
  {#each endpoints as ep (ep.path)}
    <li class="border-b p-3 last:border-b-0 border-dark-border">
      <div class="flex flex-wrap items-center gap-2">
        <span class={['shrink-0 border px-2 py-0.5 font-mono text-[11px] font-semibold', ep.tone]}>
          {ep.method}
        </span>
        <code class="break-all font-mono text-[12px] text-dark-text">
          {baseUrl}{ep.path}
        </code>
      </div>
      <p class="mt-1.5 text-sm leading-relaxed text-dark-text-secondary">
        {ep.description}
      </p>
    </li>
  {/each}
</ul>
