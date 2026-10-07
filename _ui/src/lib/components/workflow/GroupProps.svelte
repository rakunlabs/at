<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[11px] font-medium text-dark-text-muted">Color</span>
  <input
    type="color"
    bind:value={data.color}
    class="mt-0.5 w-full h-8 border border-dark-border-subtle cursor-pointer"
  /></label>
</div>
