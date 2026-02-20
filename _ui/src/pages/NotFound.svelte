<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { Home } from 'lucide-svelte';

  storeNavbar.title = '404';
</script>

<div class="flex items-center justify-center h-full">
  <div class="text-center">
    <div class="text-6xl font-bold text-gray-200 mb-2">404</div>
    <div class="text-gray-500 mb-4">Page not found</div>
    <a
      href="#/"
      class="inline-flex items-center gap-1.5 px-3 py-1.5 text-sm bg-gray-900 text-white hover:bg-gray-800 transition-colors"
    >
      <Home size={14} />
      Back to Dashboard
    </a>
  </div>
</div>
