<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';

  interface HttpRequestData {
    label?: string;
    url?: string;
    method?: string;
    headers?: Record<string, string>;
    body?: string;
    timeout?: number;
    proxy?: string;
    insecure_skip_verify?: boolean;
    retry?: boolean;
  }

  let { id, data, selected }: NodeProps<HttpRequestData> = $props();

  let urlPreview = $derived(() => {
    if (!data.url) return '';
    const maxLen = 36;
    return data.url.length > maxLen ? data.url.slice(0, maxLen) + '...' : data.url;
  });

  let flags = $derived(() => {
    const f: string[] = [];
    if (data.proxy) f.push('proxy');
    if (data.insecure_skip_verify) f.push('insecure');
    if (data.retry) f.push('retry');
    return f;
  });
</script>

<div
  class={[
    'bg-white border border-gray-300 rounded-md min-w-45 max-w-60 text-xs shadow-sm select-none',
    selected && 'border-blue-500 ring-2 ring-blue-500/25'
  ]}
>
  <HandleGroup position="left" class="!gap-1">
    <Handle id="values" type="input" port="data" accept={['data']} label="values" />
    <Handle id="data" type="input" port="data" accept={['data', 'text']} label="data" />
  </HandleGroup>
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-cyan-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-cyan-600 text-white tracking-wide">{data.method || 'GET'}</span>
    <span class="text-gray-900">{data.label || 'HTTP Request'}</span>
  </div>
  <div class="px-2.5 py-1.5 space-y-0.5">
    {#if data.url}
      <div class="flex gap-1 items-baseline">
        <span class="text-gray-400 text-[10px] shrink-0">URL:</span>
        <span class="text-gray-700 font-mono text-[10px] overflow-hidden text-ellipsis whitespace-nowrap max-w-36 inline-block">{urlPreview()}</span>
      </div>
    {:else}
      <div class="text-gray-400 text-[11px]">Configure URL</div>
    {/if}
    {#if data.timeout && data.timeout !== 30}
      <div class="flex gap-1 items-baseline">
        <span class="text-gray-400 text-[10px] shrink-0">Timeout:</span>
        <span class="text-gray-500 text-[10px]">{data.timeout}s</span>
      </div>
    {/if}
    {#if flags().length > 0}
      <div class="flex gap-1 mt-0.5 flex-wrap">
        {#each flags() as flag}
          <span class="text-[9px] px-1 py-px rounded bg-gray-100 text-gray-500 border border-gray-200">{flag}</span>
        {/each}
      </div>
    {/if}
  </div>
  <HandleGroup position="right" class="!gap-1">
    <Handle id="success" type="output" port="data" label="success" />
    <Handle id="error" type="output" port="data" label="error" />
    <Handle id="always" type="output" port="data" label="always" />
  </HandleGroup>
</div>
