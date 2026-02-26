<script lang="ts">
  import { Handle, HandleGroup, type NodeProps } from 'kaykay';

  interface EmailData {
    label?: string;
    config_id?: string;
    to?: string;
    cc?: string;
    bcc?: string;
    subject?: string;
    body?: string;
    content_type?: string;
    from?: string;
    reply_to?: string;
  }

  let { id, data, selected }: NodeProps<EmailData> = $props();

  let subjectPreview = $derived(() => {
    if (!data.subject) return '';
    const maxLen = 32;
    return data.subject.length > maxLen ? data.subject.slice(0, maxLen) + '...' : data.subject;
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
  <div class="flex items-center gap-1.5 px-2.5 py-1.5 border-b border-gray-200 font-medium bg-amber-50">
    <span class="text-[9px] font-bold px-1 py-px rounded bg-amber-600 text-white tracking-wide">SMTP</span>
    <span class="text-gray-900">{data.label || 'Email'}</span>
  </div>
  <div class="px-2.5 py-1.5 space-y-0.5">
    {#if data.to}
      <div class="flex gap-1 items-baseline">
        <span class="text-gray-400 text-[10px] shrink-0">To:</span>
        <span class="text-gray-700 font-mono text-[10px] overflow-hidden text-ellipsis whitespace-nowrap max-w-36 inline-block">{data.to}</span>
      </div>
    {/if}
    {#if data.subject}
      <div class="flex gap-1 items-baseline">
        <span class="text-gray-400 text-[10px] shrink-0">Subj:</span>
        <span class="text-gray-600 text-[10px] overflow-hidden text-ellipsis whitespace-nowrap max-w-36 inline-block">{subjectPreview()}</span>
      </div>
    {:else}
      <div class="text-gray-400 text-[11px]">Configure email</div>
    {/if}
    {#if data.content_type === 'text/html'}
      <span class="text-[9px] px-1 py-px rounded bg-gray-100 text-gray-500 border border-gray-200">HTML</span>
    {/if}
  </div>
  <HandleGroup position="right" class="!gap-1">
    <Handle id="success" type="output" port="data" label="success" />
    <Handle id="error" type="output" port="data" label="error" />
    <Handle id="always" type="output" port="data" label="always" />
  </HandleGroup>
</div>
