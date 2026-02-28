<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Color</span>
  <input
    type="color"
    bind:value={data.color}
    class="mt-0.5 w-full h-8 border border-gray-300 rounded cursor-pointer"
  /></label>
</div>
