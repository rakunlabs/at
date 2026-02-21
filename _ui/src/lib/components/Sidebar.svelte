<script lang="ts">
  import { link, location } from 'svelte-spa-router';
  import { Home, MessageSquare, Cpu, Key, BookOpen } from 'lucide-svelte';
</script>

<div class="sidebar-bg border-r border-gray-200 bg-white">
  <div class="sticky top-0 overflow-auto max-h-svh no-scrollbar">
    <a
      href="/"
      use:link
      class={[
        'flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 transition-colors',
        $location === '/' ? 'bg-gray-900 text-white' : 'text-gray-700 hover:bg-gray-100'
      ]}
    >
      <Home size={14} />
      <span>Dashboard</span>
    </a>
    <a
      href="/test"
      use:link
      class={[
        'flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 transition-colors',
        $location === '/test' ? 'bg-gray-900 text-white' : 'text-gray-700 hover:bg-gray-100'
      ]}
    >
      <MessageSquare size={14} />
      <span>Chat</span>
    </a>
    <a
      href="/providers"
      use:link
      class={[
        'flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 transition-colors',
        $location === '/providers' ? 'bg-gray-900 text-white' : 'text-gray-700 hover:bg-gray-100'
      ]}
    >
      <Cpu size={14} />
      <span>Providers</span>
    </a>
    <a
      href="/tokens"
      use:link
      class={[
        'flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 transition-colors',
        $location === '/tokens' ? 'bg-gray-900 text-white' : 'text-gray-700 hover:bg-gray-100'
      ]}
    >
      <Key size={14} />
      <span>Tokens</span>
    </a>
    <a
      href="/docs"
      use:link
      class={[
        'flex items-center gap-2 px-3 h-8 text-sm border-b border-gray-200 transition-colors',
        $location === '/docs' ? 'bg-gray-900 text-white' : 'text-gray-700 hover:bg-gray-100'
      ]}
    >
      <BookOpen size={14} />
      <span>Docs</span>
    </a>
  </div>
</div>

<style>
  /* Hide scrollbar for Chrome, Safari and Opera */
  .no-scrollbar::-webkit-scrollbar {
    display: none;
  }

  /* Hide scrollbar for IE, Edge and Firefox */
  .no-scrollbar {
    -ms-overflow-style: none; /* IE and Edge */
    scrollbar-width: none; /* Firefox */
  }
</style>
