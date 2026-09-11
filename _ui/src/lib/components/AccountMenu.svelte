<script lang="ts">
  import { storeInfo } from '@/lib/store/store.svelte';
  import { MoreHorizontal, LogOut } from 'lucide-svelte';
  import { expoOut } from 'svelte/easing';

  interface Props { onlogout?: () => Promise<void>; loggingOut?: boolean }
  let { onlogout, loggingOut = false }: Props = $props();

  let open = $state(false);
  let trigger = $state<HTMLButtonElement>();
  let panel = $state<HTMLDivElement>();

  // An empty menu is a dead control, so the trigger only exists when it has
  // something to hold.
  const hasContent = $derived(!!(storeInfo.name || storeInfo.user || onlogout));

  const items = () =>
    Array.from(panel?.querySelectorAll<HTMLElement>('[role="menuitem"]:not([disabled])') ?? []);

  const focusAt = (index: number) => {
    const list = items();
    if (!list.length) return;
    list[(index + list.length) % list.length].focus();
  };

  const close = (returnFocus = true) => {
    open = false;
    if (returnFocus) trigger?.focus();
  };

  // Opening from the keyboard or the pointer both land on the first item, per
  // the WAI-ARIA menu button pattern.
  const toggle = async () => {
    if (open) return close();
    open = true;
    await Promise.resolve();
    focusAt(0);
  };

  const onTriggerKeydown = (event: KeyboardEvent) => {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    event.preventDefault();
    if (!open) {
      void toggle();
      return;
    }
    focusAt(event.key === 'ArrowDown' ? 0 : items().length - 1);
  };

  const onMenuKeydown = (event: KeyboardEvent) => {
    const list = items();
    const current = list.indexOf(document.activeElement as HTMLElement);
    switch (event.key) {
      case 'Escape':
        event.preventDefault();
        close();
        break;
      case 'ArrowDown':
        event.preventDefault();
        focusAt(current + 1);
        break;
      case 'ArrowUp':
        event.preventDefault();
        focusAt(current - 1);
        break;
      case 'Home':
        event.preventDefault();
        focusAt(0);
        break;
      case 'End':
        event.preventDefault();
        focusAt(list.length - 1);
        break;
      case 'Tab':
        // Let focus leave naturally rather than trapping it in a small menu.
        close(false);
        break;
    }
  };

  $effect(() => {
    if (!open) return;
    const onPointerDown = (event: PointerEvent) => {
      const target = event.target as Node;
      if (panel?.contains(target) || trigger?.contains(target)) return;
      close(false);
    };
    document.addEventListener('pointerdown', onPointerDown, true);
    return () => document.removeEventListener('pointerdown', onPointerDown, true);
  });

  const reveal = (_: HTMLElement, { duration = 140 }: { duration?: number }) => {
    const reduced =
      typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
    return {
      duration: reduced ? 0 : duration,
      easing: expoOut,
      css: (t: number, u: number) =>
        `opacity:${t};transform:translateY(${u * -6}px) scale(${1 - u * 0.03});transform-origin:top right`
    };
  };

  const signOut = async () => {
    if (!onlogout || loggingOut) return;
    close(false);
    await onlogout();
  };
</script>

{#if hasContent}
  <div class="relative shrink-0">
    <button
      bind:this={trigger}
      type="button"
      aria-haspopup="menu"
      aria-expanded={open}
      aria-controls="account-menu"
      aria-label={storeInfo.user ? `Account menu for ${storeInfo.user}` : 'Account menu'}
      onclick={toggle}
      onkeydown={onTriggerKeydown}
      class="p-1.5 text-gray-500 dark:text-dark-text-muted hover:bg-gray-100 dark:hover:bg-dark-elevated hover:text-gray-900 dark:hover:text-dark-text focus-visible:outline-2 focus-visible:outline-accent transition-colors {open
        ? 'bg-gray-100 dark:bg-dark-elevated text-gray-900 dark:text-dark-text'
        : ''}"
    >
      <MoreHorizontal size={16} />
    </button>

    {#if open}
      <div
        bind:this={panel}
        transition:reveal={{}}
        class="absolute right-0 top-full z-50 mt-1 w-64 border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface shadow-lg"
      >
        {#if storeInfo.name || storeInfo.user}
          <div class="border-b border-gray-200 dark:border-dark-border px-3 py-2.5">
            {#if storeInfo.name}
              <p class="flex items-baseline gap-1.5 text-sm font-semibold text-gray-900 dark:text-dark-text">
                <span class="truncate">{storeInfo.name}</span>
                {#if storeInfo.version}
                  <span class="shrink-0 text-xs font-normal tabular-nums text-gray-600 dark:text-dark-text-secondary">
                    {storeInfo.version}
                  </span>
                {/if}
              </p>
            {/if}
            {#if storeInfo.user}
              <p
                class="mt-0.5 truncate text-xs text-gray-600 dark:text-dark-text-secondary"
                title={storeInfo.user}
              >
                {storeInfo.user}
              </p>
            {/if}
          </div>
        {/if}

        <!--
          Focus only ever rests on a menu item, so the menu owns the keys.
          tabindex="-1" keeps the container out of the tab order while still
          satisfying the interactive-role focus requirement.
        -->
        <div
          id="account-menu"
          role="menu"
          aria-label="Account"
          tabindex="-1"
          class="py-1 focus:outline-none"
          onkeydown={onMenuKeydown}
        >
          {#if onlogout}
            <button
              type="button"
              role="menuitem"
              disabled={loggingOut}
              onclick={signOut}
              class="flex w-full items-center gap-2 px-3 py-2 text-left text-sm text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated hover:text-gray-900 dark:hover:text-dark-text focus-visible:bg-gray-100 dark:focus-visible:bg-dark-elevated focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
            >
              <LogOut size={14} class="shrink-0" />
              <span>{loggingOut ? 'Signing out…' : 'Sign out'}</span>
            </button>
          {/if}
        </div>
      </div>
    {/if}
  </div>
{/if}
