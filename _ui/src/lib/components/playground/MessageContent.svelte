<script lang="ts">
  import { FileText, ImageOff } from 'lucide-svelte';
  import type { ChatMessage, ContentPart } from '@/lib/helper/chat';
  import { mediaImageURL } from '@/lib/api/media';
  import Markdown from '@/lib/components/Markdown.svelte';
  import ImageLightbox from '@/lib/components/ImageLightbox.svelte';
  import { mediaRefIDs, messageText, resolveMediaRefs } from '@/lib/helper/media-ref';

  interface Props {
    message: ChatMessage;
    workspace: string;
    raw?: boolean;
    thinking?: boolean;
    formatSize: (bytes: number) => string;
  }
  let { message, workspace, raw = false, thinking = false, formatSize }: Props = $props();
  const user = $derived(message.role === 'user');
  // Media the answer already places through `media:<id>` Markdown is shown
  // there; its stored part stays in the message (history, shares, copies)
  // but is not rendered a second time below the text.
  const placed = $derived(user ? new Set<string>() : mediaRefIDs(messageText(message.content)));
  const resolve = (text: string) => resolveMediaRefs(text, id => mediaImageURL(id, workspace));

  // Raw view is text only: stored media is described, never rendered. Inline
  // data URLs are not printed, since they can be megabytes of base64.
  function rawPartLabel(part: ContentPart): string {
    const fields: string[] = [part.type === 'image_url' ? 'image' : part.type];
    if (part.name || part.file?.filename) fields.push(part.name || part.file!.filename!);
    if (part.mime_type) fields.push(part.mime_type);
    if (part.media_id) fields.push(`media:${part.media_id}`);
    else if (part.type === 'image_url') {
      const url = part.image_url?.url || '';
      fields.push(url.startsWith('data:') ? 'inline data' : url);
    } else if (part.omitted) fields.push('not saved to history');
    if (part.bytes) fields.push(formatSize(part.bytes));
    return `[${fields.join(' · ')}]`;
  }

  let zoomed = $state<{ src: string; alt: string } | null>(null);

  // Images inside rendered Markdown (e.g. a generated image shown as ![](url))
  // are plain <img> elements, so they are opened through delegation.
  function zoomMarkdownImage(e: MouseEvent) {
    const target = e.target;
    if (!(target instanceof HTMLImageElement) || target.closest('a')) return;
    e.preventDefault();
    zoomed = { src: target.currentSrc || target.src, alt: target.alt };
  }

  // While an answer streams, its Markdown is re-rendered on every delta and
  // each <img> is recreated, so a request can be cut off and leave a broken
  // image that only a later re-render (e.g. Raw and back) repairs. Retry a
  // failed Markdown image once on its own. `error` does not bubble, hence the
  // capturing listener.
  function retryMarkdownImage(node: HTMLElement) {
    const onError = (e: Event) => {
      const img = e.target;
      if (!(img instanceof HTMLImageElement) || img.dataset.retried) return;
      img.dataset.retried = '1';
      const src = img.getAttribute('src');
      if (!src) return;
      setTimeout(() => { if (img.isConnected) { img.removeAttribute('src'); img.setAttribute('src', src); } }, 1000);
    };
    node.addEventListener('error', onError, true);
    return { destroy: () => node.removeEventListener('error', onError, true) };
  }
</script>

{#snippet markdown(source: string)}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="contents [&_.markdown-body_img]:cursor-zoom-in" onclick={zoomMarkdownImage} use:retryMarkdownImage><Markdown source={resolve(source)} /></div>
{/snippet}

{#snippet omitted(part: ContentPart)}
  <div class={['mb-2 flex items-center gap-1.5 border border-dashed px-2 py-1 text-[11px]', user ? 'border-dark-border text-white/80' : 'border-dark-border text-dark-text-muted']}>
    <ImageOff size={11} class="shrink-0" />
    <span class="truncate">{part.name || (part.type === 'file' ? 'attachment' : 'image')} — {part.type === 'file' ? 'attachment' : 'image'} not saved to history</span>
  </div>
{/snippet}

{#snippet pendingUpload(part: ContentPart)}
  {@const url = part.type === 'video_url' ? part.video_url?.url : part.type === 'file' ? part.file?.file_data : part.input_audio ? `data:audio/${part.input_audio.format === 'mp3' ? 'mpeg' : part.input_audio.format};base64,${part.input_audio.data}` : ''}
  <div class="mb-2 border border-dark-border/5">
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
  <div class="my-2 border border-dark-border bg-dark-base">
    {#if type === 'application/pdf'}
      <!-- The server sends CSP: sandbox and nosniff; an iframe sandbox would
           also block Chrome's PDF viewer. -->
      <iframe src={url} title={part.name || 'PDF'} class="w-full h-96 bg-dark-surface"></iframe>
    {:else if type.startsWith('audio/')}
      <audio controls src={url} class="w-full p-2"></audio>
    {:else if type.startsWith('video/')}
      <!-- svelte-ignore a11y_media_has_caption -->
      <video controls src={url} class="w-full max-h-96"></video>
    {/if}
    <div class="flex items-center gap-2 px-3 py-2 text-xs">
      <FileText size={13} class="shrink-0 text-dark-text-muted" />
      <span class="truncate font-medium text-dark-text">{part.name || 'file'}</span>
      <span class="shrink-0 text-[10px] text-dark-text-muted">{type || 'file'}{part.bytes ? ` · ${formatSize(part.bytes)}` : ''}</span>
      <a href={url} download={part.name || 'file'} class="ml-auto shrink-0 border border-dark-border-subtle px-2 py-1 text-dark-text-secondary hover:bg-dark-elevated">Download</a>
    </div>
  </div>
{/snippet}

{#if typeof message.content === 'string'}
  {#if user}
    <span class="whitespace-pre-wrap">{message.content}</span>
  {:else if !message.content && thinking}
    <span class="text-dark-text-muted italic">Thinking...</span>
  {:else if raw}
    <pre class="whitespace-pre-wrap break-words font-mono text-xs">{message.content}</pre>
  {:else}
    {@render markdown(message.content)}
  {/if}
{:else}
  {#each message.content as part}
    {#if raw && !user && part.type !== 'text'}
      <pre class="mb-2 whitespace-pre-wrap break-words font-mono text-xs text-dark-text-muted">{rawPartLabel(part)}</pre>
    {:else if (part.type === 'image' || part.type === 'file') && part.media_id && placed.has(part.media_id)}
      <!-- Shown where the answer references it. -->
    {:else if (part.type === 'image_url' && part.image_url?.url) || (part.type === 'image' && part.media_id)}
      {@const src = part.type === 'image' ? mediaImageURL(part.media_id!, workspace) : part.image_url!.url}
      {@const alt = part.type === 'image' ? part.name || 'Stored image attachment' : ''}
      <button
        type="button"
        onclick={() => (zoomed = { src, alt: alt || part.name || 'image' })}
        aria-label={`Enlarge ${alt || 'image'}`}
        class="mb-2 block max-w-full cursor-zoom-in focus-visible:outline-2 focus-visible:outline-accent"
      >
        <img
          {src}
          {alt}
          loading={part.type === 'image' ? 'lazy' : undefined}
          class={['block max-w-full max-h-64 border', user ? 'border-accent/50' : 'border-dark-border']}
        />
      </button>
    {:else if part.type === 'image'}
      {@render omitted(part)}
    {:else if part.type === 'file' && part.media_id}
      {#if user}<div class="text-dark-text">{@render fileArtifact(part)}</div>{:else}{@render fileArtifact(part)}{/if}
    {:else if user && ((part.type === 'file' && part.file) || part.type === 'input_audio' || part.type === 'video_url')}
      {@render pendingUpload(part)}
    {:else if user && part.type === 'file'}
      {@render omitted(part)}
    {:else if user && part.type === 'text' && part.text?.startsWith('<file name="')}
      {@const fileName = /^<file name="([^"]*)"/.exec(part.text)?.[1] || 'file'}
      <div class="mb-2 flex items-center gap-1.5 border border-dark-border px-2 py-1 text-[11px] text-white/80">
        <FileText size={11} class="shrink-0" /><span class="truncate">{fileName}</span>
      </div>
    {:else if part.type === 'text' && part.text}
      {#if user}<span class="whitespace-pre-wrap">{part.text}</span>{:else if raw}<pre class="whitespace-pre-wrap break-words font-mono text-xs">{part.text}</pre>{:else}{@render markdown(part.text)}{/if}
    {/if}
  {/each}
{/if}

{#if zoomed}
  <ImageLightbox src={zoomed.src} alt={zoomed.alt} onclose={() => (zoomed = null)} />
{/if}
