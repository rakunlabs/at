<script lang="ts">
  /*
   * Full-screen auth gates (sign-in, first setup, recovery, backup codes,
   * mobile approval, connection splash) render outside the app shell, so they
   * used to carry their own layout: a bare centred column with a `text-2xl`
   * heading and rounded panels. That was a second design system — the app
   * itself is square, compact and card-based — and it was the first thing a
   * user saw. This component reproduces the reference card (Providers, Agents,
   * Secrets, Features, Tokens) for those screens so the style does not change
   * between the login page and the page behind it.
   */
  import type { Snippet } from 'svelte';
  import BrandLogo from './BrandLogo.svelte';

  interface Props {
    title: string;
    /** Caption under the title. Keep it one short line; longer copy belongs in the body. */
    subtitle?: string;
    /** Card width: forms are narrow, review screens (approval, backup codes) need more room. */
    width?: 'sm' | 'md' | 'lg';
    /** Accessible name when it should differ from the visible title. */
    label?: string;
    /** Small control pinned to the right of the header strip (e.g. a disclosure). */
    action?: Snippet;
    children: Snippet;
  }

  let { title, subtitle = '', width = 'sm', label = '', action, children }: Props = $props();

  const widths = { sm: 'max-w-sm', md: 'max-w-md', lg: 'max-w-lg' };
</script>

<main class="min-h-full overflow-y-auto bg-gray-50 dark:bg-dark-base flex items-start sm:items-center justify-center p-4 sm:p-6">
  <div class={['w-full', widths[width]]}>
    <!-- Same brand mark and wordmark as the sidebar, so the gate reads as this
         installation rather than an anonymous form. -->
    <div class="mb-3 flex items-center gap-2 text-base font-semibold text-gray-900 dark:text-dark-text">
      <BrandLogo size={24} decorative />AT
    </div>
    <section
      aria-label={label || title}
      class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface"
    >
      <div class="px-4 py-3 border-b border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base flex items-start gap-2">
        <div class="min-w-0 flex-1">
          <h1 class="text-sm font-medium text-gray-900 dark:text-dark-text break-words">{title}</h1>
          {#if subtitle}<p class="settings-note mt-0.5">{subtitle}</p>{/if}
        </div>
        {#if action}<div class="shrink-0">{@render action()}</div>{/if}
      </div>
      <div class="p-4 space-y-4 settings-form">{@render children()}</div>
    </section>
  </div>
</main>
