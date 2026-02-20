<script lang="ts">
  import { storeNavbar } from '@/lib/store/store.svelte';
  import { ChevronLeft, Menu } from 'lucide-svelte';
</script>

<div class="bg-white border-b border-gray-200 flex items-center px-2 h-full">
  <button
    class="p-1 hover:bg-gray-100 text-gray-500 hover:text-gray-900 transition-colors"
    onclick={() => (storeNavbar.sideBarOpen = !storeNavbar.sideBarOpen)}
  >
    {#if storeNavbar.sideBarOpen}
      <ChevronLeft size={16} />
    {:else}
      <Menu size={16} />
    {/if}
  </button>
  <span class="ml-2 text-sm font-medium text-gray-800">
    {storeNavbar.title}
  </span>
</div>
