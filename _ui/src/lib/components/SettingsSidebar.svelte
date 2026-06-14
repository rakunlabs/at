<script lang="ts">
  import { push, location } from 'svelte-spa-router';
  import { KeyRound, Settings, ToggleLeft } from 'lucide-svelte';

  const items = [
    { path: '/settings', label: 'General', exact: true },
    { path: '/settings/features', label: 'Features' },
    { path: '/settings/tokens', label: 'API Tokens' },
  ];

  function navigate(e: MouseEvent, path: string) {
    if (e.ctrlKey || e.metaKey || e.shiftKey) return;
    e.preventDefault();
    push(path);
  }

  function isActive(path: string, exact?: boolean) {
    return exact ? $location === path : $location === path || $location.startsWith(`${path}/`);
  }
</script>

<aside class="border-r border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface min-h-full">
  <nav class="space-y-1">
    {#each items as item}
      <a
        href={`#${item.path}`}
        onclick={(e) => navigate(e, item.path)}
        class={[
          'flex items-center gap-2 px-2.5 py-2 text-sm border transition-colors',
          isActive(item.path, item.exact)
            ? 'bg-gray-900 dark:bg-accent text-white border-gray-900 dark:border-accent'
            : 'bg-white dark:bg-dark-surface text-gray-700 dark:text-dark-text-secondary border-transparent hover:bg-gray-50 dark:hover:bg-dark-elevated',
        ]}
      >
        {#if item.label === 'General'}
          <Settings size={14} />
        {:else if item.label === 'Features'}
          <ToggleLeft size={14} />
        {:else}
          <KeyRound size={14} />
        {/if}
        <span>{item.label}</span>
      </a>
    {/each}
  </nav>
</aside>
