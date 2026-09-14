<script lang="ts">
  interface Props { size?: number; decorative?: boolean }
  let { size = 24, decorative = false }: Props = $props();
</script>

<img src="./brand/favicon.svg" alt={decorative ? '' : 'AT'} width={size} height={size} class="shrink-0 object-contain" />
