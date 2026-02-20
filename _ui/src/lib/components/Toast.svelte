<script lang="ts">
  import { removeToast, storeToast } from '@/lib/store/toast.svelte';
  import { X } from 'lucide-svelte';

  const close = (id: number) => {
    removeToast(id);
  };
  const customSlide = (_: HTMLElement, { duration }: { duration: number }) => {
    return {
      duration,
      css: (_: number, u: number) => `transform: translateX(${u * 400}px)`
    };
  };
</script>

<div class="fixed bottom-4 right-4 z-50 flex flex-col gap-2">
  {#each storeToast as toast (toast.id)}
    <div
      class={`toast-${toast.type} flex items-center gap-2 px-3 py-2 shadow-lg border text-sm max-w-sm`}
      transition:customSlide={{ duration: 200 }}
    >
      <span class="flex-1">{toast.message}</span>
      <button onclick={() => close(toast.id)} class="shrink-0 p-0.5 hover:bg-black/10 transition-colors">
        <X size={14} />
      </button>
    </div>
  {/each}
</div>

<style>
  @reference "tailwindcss";

  .toast-alert {
    @apply bg-red-100 text-red-900 border-red-300;
  }

  .toast-info {
    @apply bg-emerald-100 text-emerald-900 border-emerald-300;
  }

  .toast-warn {
    @apply bg-amber-100 text-amber-900 border-amber-300;
  }
</style>
