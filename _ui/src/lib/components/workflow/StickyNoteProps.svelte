<script lang="ts">
  let { data }: { data: Record<string, any> } = $props();
</script>

<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Text (Markdown)</span>
  <textarea
    bind:value={data.text}
    rows={5}
    class="mt-0.5 w-full px-2 py-1 text-xs border border-gray-300 rounded focus:outline-none focus:ring-1 focus:ring-gray-400 resize-y"
    placeholder="**Bold**, _italic_, `code`, [link](url)"
  ></textarea></label>
</div>
<div>
  <label class="block">
    <span class="text-[10px] font-medium text-gray-500 uppercase tracking-wider">Color</span>
  <input
    type="color"
    bind:value={data.color}
    class="mt-0.5 w-full h-8 border border-gray-300 rounded cursor-pointer"
  /></label>
</div>
