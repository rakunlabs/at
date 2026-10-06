<script lang="ts">
  import { getInfo, type InfoProvider } from '@/lib/api/gateway';
  import type { ImageGenerationConfig } from '@/lib/api/mcp-servers';

  interface Props {
    value: ImageGenerationConfig;
  }

  let { value = $bindable() }: Props = $props();

  let providers = $state<InfoProvider[]>([]);
  let loaded = $state(false);

  getInfo()
    .then((info) => {
      providers = (info.providers || []).filter((p) => p.type === 'openai' || p.type === 'minimax');
    })
    .catch(() => {})
    .finally(() => (loaded = true));

  const providerRef = (p: InfoProvider) => p.reference || p.key;
  const selected = $derived(providers.find((p) => providerRef(p) === value.provider));
  const modelOptions = $derived(
    selected?.type === 'minimax' ? ['image-01'] : ['gpt-image-2', 'gpt-image-1.5', 'gpt-image-1', 'dall-e-3'],
  );
  const autoProvider = $derived(providers.length === 1 ? providerRef(providers[0]) : '');

  const field =
    'w-full border border-dark-border-subtle px-3 py-1.5 text-sm focus:outline-none focus:ring-2 focus:ring-accent/20 focus:border-dark-border-subtle bg-dark-elevated text-dark-text placeholder:text-dark-text-muted';
  const label = 'text-xs text-dark-text-muted';
</script>

<div class="space-y-2">
  <div class="grid grid-cols-1 sm:grid-cols-2 gap-2">
    <label class="block">
      <span class={label}>Provider</span>
      <select bind:value={value.provider} class={field}>
        <option value="">
          {autoProvider ? `Automatic (${autoProvider})` : 'Caller chooses'}
        </option>
        {#each providers as p (providerRef(p))}
          <option value={providerRef(p)}>{p.key} · {p.type}</option>
        {/each}
        {#if value.provider && !selected && loaded}
          <option value={value.provider}>{value.provider} (unavailable)</option>
        {/if}
      </select>
    </label>
    <label class="block">
      <span class={label}>Model</span>
      <input
        bind:value={value.model}
        list="image-generation-models"
        placeholder={value.provider || autoProvider ? 'Provider default' : 'Caller chooses'}
        class={field}
      />
      <datalist id="image-generation-models">
        {#each modelOptions as m}<option value={m}></option>{/each}
      </datalist>
    </label>
    <label class="block">
      <span class={label}>Default size</span>
      <select bind:value={value.size} class={field}>
        <option value="">Provider default</option>
        <option value="1024x1024">1024x1024 (square)</option>
        <option value="1536x1024">1536x1024 (landscape)</option>
        <option value="1024x1536">1024x1536 (portrait)</option>
        <option value="auto">auto</option>
      </select>
    </label>
    <label class="block">
      <span class={label}>Default quality</span>
      <select bind:value={value.quality} class={field}>
        <option value="">Provider default</option>
        <option value="low">low</option>
        <option value="medium">medium</option>
        <option value="high">high</option>
        <option value="auto">auto</option>
      </select>
    </label>
    <label class="block">
      <span class={label}>Default background</span>
      <select bind:value={value.background} class={field}>
        <option value="">Provider default</option>
        <option value="auto">auto</option>
        <option value="opaque">opaque</option>
        <option value="transparent">transparent</option>
      </select>
    </label>
  </div>
  {#if loaded && providers.length === 0}
    <p class="text-xs text-amber-400">No image-capable provider (OpenAI or MiniMax) in this workspace yet; add one under Providers.</p>
  {/if}
  <p class="text-xs text-dark-text-muted">
    A chosen provider and model are removed from the tool's arguments, so clients such as OpenCode never have to guess them. Size, quality and background are defaults the caller may still override.
  </p>
</div>
