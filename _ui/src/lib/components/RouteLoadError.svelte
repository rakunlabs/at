<script lang="ts">
  function reload() { window.location.reload(); }
</script>

<div role="alert" class="m-4 border border-dark-border">
  <div class="px-4 py-3 border-b border-dark-border text-sm font-semibold">Page could not be downloaded</div>
  <div class="p-4 text-sm text-dark-text-secondary">
    <p>Check your connection and reload. A server update may also require a fresh page.</p>
    <button onclick={reload} class="mt-3 border border-dark-border-subtle px-3 py-1.5 text-xs hover:bg-dark-elevated">Reload this page</button>
  </div>
</div>
