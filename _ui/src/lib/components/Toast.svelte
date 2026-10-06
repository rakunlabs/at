<script lang="ts">
  import { removeToast, storeToast } from '@/lib/store/toast.svelte';
  import { X } from 'lucide-svelte';

  const close = (id: number) => {
    removeToast(id);
  };
</script>

<div class="fixed bottom-4 right-4 z-[200] flex flex-col gap-2">
  {#each storeToast as toast (toast.id)}
    <div
      class={`toast-${toast.type} flex items-center gap-2 px-3 py-2 shadow-lg border text-sm max-w-sm`}
    >
      <span class="flex-1">{toast.message}</span>
      <button onclick={() => close(toast.id)} class="shrink-0 p-0.5 hover:bg-white/10">
        <X size={14} />
      </button>
    </div>
  {/each}
</div>

<style>
  @reference "../../style/global.css";

  .toast-alert {
    @apply bg-red-900/40 text-red-200 border-red-800;
  }

  .toast-info {
    @apply bg-emerald-900/40 text-emerald-200 border-emerald-800;
  }

  .toast-warn {
    @apply bg-amber-900/40 text-amber-200 border-amber-800;
  }
</style>
