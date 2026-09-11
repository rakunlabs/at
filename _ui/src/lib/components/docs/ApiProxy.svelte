<script lang="ts">
  import { ArrowDown } from 'lucide-svelte';

  interface Props {
    baseUrl: string;
  }

  let { baseUrl }: Props = $props();
</script>

<p class="text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
  Access any provider endpoint directly via the gateway. The gateway handles authentication and
  credential injection, then forwards the request unchanged.
</p>

<div class="border border-gray-200 dark:border-dark-border bg-white dark:bg-dark-surface p-3">
  <div class="flex flex-wrap items-center gap-2">
    <span
      class="shrink-0 border border-gray-300 bg-gray-100 px-2 py-0.5 font-mono text-[11px] font-semibold text-gray-700 dark:border-dark-border-subtle dark:bg-dark-elevated dark:text-dark-text-secondary"
    >
      ANY
    </span>
    <code class="break-all font-mono text-[12px] text-gray-800 dark:text-dark-text">
      {baseUrl}/gateway/proxy/:provider/:path*
    </code>
  </div>
  <p class="mt-1.5 text-sm leading-relaxed text-gray-600 dark:text-dark-text-secondary">
    Forwards requests to the specified provider.
  </p>
</div>

<div class="border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-elevated p-3">
  <p class="text-[11px] font-medium uppercase tracking-wider text-gray-600 dark:text-dark-text-secondary">
    Example: Gemini file search
  </p>
  <p class="mt-2 break-all font-mono text-[12px] text-gray-800 dark:text-dark-text">
    POST {baseUrl}/gateway/proxy/gemini/v1beta/files
  </p>
  <p class="mt-1 flex items-center gap-1.5 text-[11px] text-gray-600 dark:text-dark-text-secondary">
    <ArrowDown size={12} aria-hidden="true" />
    forwards to
  </p>
  <p class="mt-1 break-all font-mono text-[12px] text-gray-800 dark:text-dark-text">
    https://generativelanguage.googleapis.com/v1beta/files
  </p>
</div>
