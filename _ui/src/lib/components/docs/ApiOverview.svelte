<script lang="ts">
  import { ArrowRight } from 'lucide-svelte';
  import { apiGroups, sectionsInGroup } from './api-sections';

  interface Props {
    baseUrl: string;
    modelCount: number;
    providerCount: number;
    loading?: boolean;
  }

  let { baseUrl, modelCount, providerCount, loading = false }: Props = $props();

  const facts = $derived([
    { label: 'OpenAI base URL', value: `${baseUrl}/gateway/v1` },
    { label: 'Anthropic base URL', value: `${baseUrl}/gateway` },
    { label: 'Authentication', value: 'Authorization: Bearer at_…' },
    {
      label: 'This instance',
      value: loading
        ? 'Loading…'
        : `${providerCount} provider${providerCount === 1 ? '' : 's'} · ${modelCount} model${modelCount === 1 ? '' : 's'}`,
    },
  ]);
</script>

<div class="docs-prose">
  <p>
    AT is a single gateway in front of every model provider you configure — OpenAI, Anthropic,
    Gemini, Vertex, Bedrock, Azure and any OpenAI-compatible server. Clients speak the
    <strong>OpenAI</strong> or <strong>Anthropic</strong> HTTP API they already know; AT
    authenticates the token, picks the provider, applies budgets and fallbacks, and records a trace
    of the call.
  </p>
  <p>
    Models are addressed as <code>provider_key/model_name</code>, for example
    <code>openai/gpt-4o</code>. A bare name without a slash is looked up as a
    <a href="#/docs?section=routing">routing profile</a>.
  </p>
</div>

<dl class="grid gap-px border border-dark-border bg-dark-border sm:grid-cols-2">
  {#each facts as f (f.label)}
    <div class="bg-dark-base px-3 py-2.5">
      <dt class="text-[11px] text-dark-text-muted">{f.label}</dt>
      <dd class="mt-1 break-all font-mono text-[12.5px] text-dark-text">{f.value}</dd>
    </div>
  {/each}
</dl>

<div class="space-y-6 pt-2">
  {#each apiGroups as group (group.id)}
    <section>
      <h2 class="text-sm font-semibold text-dark-text">{group.title}</h2>
      <p class="mt-0.5 text-xs text-dark-text-muted">{group.description}</p>
      <ul class="mt-3 grid gap-px border border-dark-border bg-dark-border sm:grid-cols-2">
        {#each sectionsInGroup(group.id).filter((s) => s.id !== 'overview') as s (s.id)}
          <li class="bg-dark-base">
            <a
              href={`#/docs?section=${s.id}`}
              class="group flex h-full items-start gap-2 px-3 py-2.5 hover:bg-dark-surface focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent"
            >
              <span class="min-w-0 flex-1">
                <span class="block text-sm text-dark-text">{s.title}</span>
                <span class="mt-0.5 block text-xs leading-relaxed text-dark-text-muted">
                  {s.description}
                </span>
              </span>
              <ArrowRight
                size={13}
                class="mt-1 shrink-0 text-dark-text-faint group-hover:text-accent-text"
                aria-hidden="true"
              />
            </a>
          </li>
        {/each}
      </ul>
    </section>
  {/each}
</div>
