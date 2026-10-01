<script lang="ts">
  import { FileText, ImageOff } from 'lucide-svelte';
  import type { ChatMessage, ContentPart } from '@/lib/helper/chat';
  import { mediaImageURL } from '@/lib/api/media';
  import Markdown from '@/lib/components/Markdown.svelte';

  interface Props {
    message: ChatMessage;
    workspace: string;
    raw?: boolean;
    thinking?: boolean;
    formatSize: (bytes: number) => string;
  }
  let { message, workspace, raw = false, thinking = false, formatSize }: Props = $props();
  const user = $derived(message.role === 'user');
</script>

{#snippet omitted(part: ContentPart)}
  <div class={['mb-2 flex items-center gap-1.5 border border-dashed px-2 py-1 text-[11px]', user ? 'border-white/40 text-white/80' : 'border-gray-300 dark:border-dark-border text-gray-500 dark:text-dark-text-muted']}>
    <ImageOff size={11} class="shrink-0" />
    <span class="truncate">{part.name || (part.type === 'file' ? 'attachment' : 'image')} — {part.type === 'file' ? 'attachment' : 'image'} not saved to history</span>
  </div>
{/snippet}

{#snippet pendingUpload(part: ContentPart)}
  {@const url = part.type === 'video_url' ? part.video_url?.url : part.type === 'file' ? part.file?.file_data : part.input_audio ? `data:audio/${part.input_audio.format === 'mp3' ? 'mpeg' : part.input_audio.format};base64,${part.input_audio.data}` : ''}
  <div class="mb-2 border border-white/40 bg-white/5">
    {#if part.type === 'input_audio' && url}
      <audio controls src={url} class="w-full p-1"></audio>
    {:else if part.type === 'video_url' && url}
      <!-- svelte-ignore a11y_media_has_caption -->
      <video controls src={url} class="w-full max-h-64"></video>
    {/if}
    <div class="flex items-center gap-1.5 px-2 py-1 text-[11px] text-white/80">
      <FileText size={11} class="shrink-0" />
      <span class="truncate">{part.file?.filename || (part.type === 'input_audio' ? 'audio' : part.type === 'video_url' ? 'video' : 'attachment')}</span>
    </div>
  </div>
{/snippet}

{#snippet fileArtifact(part: ContentPart)}
  {@const url = mediaImageURL(part.media_id!, workspace)}
  {@const type = part.mime_type || ''}
  <div class="my-2 border border-gray-200 dark:border-dark-border bg-gray-50 dark:bg-dark-base">
    {#if type === 'application/pdf'}
      <!-- The server sends CSP: sandbox and nosniff; an iframe sandbox would
           also block Chrome's PDF viewer. -->
      <iframe src={url} title={part.name || 'PDF'} class="w-full h-96 bg-white"></iframe>
    {:else if type.startsWith('audio/')}
      <audio controls src={url} class="w-full p-2"></audio>
    {:else if type.startsWith('video/')}
      <!-- svelte-ignore a11y_media_has_caption -->
      <video controls src={url} class="w-full max-h-96"></video>
    {/if}
    <div class="flex items-center gap-2 px-3 py-2 text-xs">
      <FileText size={13} class="shrink-0 text-gray-500 dark:text-dark-text-muted" />
      <span class="truncate font-medium text-gray-700 dark:text-dark-text">{part.name || 'file'}</span>
      <span class="shrink-0 text-[10px] text-gray-400 dark:text-dark-text-muted">{type || 'file'}{part.bytes ? ` · ${formatSize(part.bytes)}` : ''}</span>
      <a href={url} download={part.name || 'file'} class="ml-auto shrink-0 border border-gray-300 dark:border-dark-border-subtle px-2 py-1 text-gray-700 dark:text-dark-text-secondary hover:bg-gray-100 dark:hover:bg-dark-elevated">Download</a>
    </div>
  </div>
{/snippet}

{#if typeof message.content === 'string'}
  {#if user}
    <span class="whitespace-pre-wrap">{message.content}</span>
  {:else if !message.content && thinking}
    <span class="text-gray-400 dark:text-dark-text-muted italic">Thinking...</span>
  {:else if raw}
    <pre class="whitespace-pre-wrap break-words font-mono text-xs">{message.content}</pre>
  {:else}
    <Markdown source={message.content} />
  {/if}
{:else}
  {#each message.content as part}
    {#if (part.type === 'image_url' && part.image_url?.url) || (part.type === 'image' && part.media_id)}
      <img
        src={part.type === 'image' ? mediaImageURL(part.media_id!, workspace) : part.image_url!.url}
        alt={part.type === 'image' ? part.name || 'Stored image attachment' : ''}
        loading={part.type === 'image' ? 'lazy' : undefined}
        class={['max-w-full max-h-64 mb-2 border', user ? 'border-gray-600 dark:border-accent/50' : 'border-gray-200 dark:border-dark-border']}
      />
    {:else if part.type === 'image'}
      {@render omitted(part)}
    {:else if part.type === 'file' && part.media_id}
      {#if user}<div class="text-gray-800 dark:text-dark-text">{@render fileArtifact(part)}</div>{:else}{@render fileArtifact(part)}{/if}
    {:else if user && ((part.type === 'file' && part.file) || part.type === 'input_audio' || part.type === 'video_url')}
      {@render pendingUpload(part)}
    {:else if user && part.type === 'file'}
      {@render omitted(part)}
    {:else if user && part.type === 'text' && part.text?.startsWith('<file name="')}
      {@const fileName = /^<file name="([^"]*)"/.exec(part.text)?.[1] || 'file'}
      <div class="mb-2 flex items-center gap-1.5 border border-white/40 px-2 py-1 text-[11px] text-white/80">
        <FileText size={11} class="shrink-0" /><span class="truncate">{fileName}</span>
      </div>
    {:else if part.type === 'text' && part.text}
      {#if user}<span class="whitespace-pre-wrap">{part.text}</span>{:else if raw}<pre class="whitespace-pre-wrap break-words font-mono text-xs">{part.text}</pre>{:else}<Markdown source={part.text} />{/if}
    {/if}
  {/each}
{/if}
