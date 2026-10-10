<script lang="ts">
  // Name / type / description table for request fields, headers and status
  // codes. Descriptions are plain text; keep markup out of the data.
  import type { DocsParam } from './docs-types';

  interface Props {
    params: DocsParam[];
    /** Column heading for the first column. */
    nameLabel?: string;
    /** Hide the type column when every row would be empty. */
    showType?: boolean;
  }

  let { params, nameLabel = 'Field', showType = true }: Props = $props();
</script>

<div class="overflow-x-auto border border-dark-border">
  <table class="w-full border-collapse text-left text-xs">
    <thead>
      <tr class="border-b border-dark-border bg-dark-surface text-dark-text-muted">
        <th scope="col" class="px-3 py-1.5 font-medium">{nameLabel}</th>
        {#if showType}<th scope="col" class="px-3 py-1.5 font-medium">Type</th>{/if}
        <th scope="col" class="px-3 py-1.5 font-medium">Description</th>
      </tr>
    </thead>
    <tbody>
      {#each params as p (p.name)}
        <tr class="border-b border-dark-border align-top last:border-b-0">
          <td class="whitespace-nowrap px-3 py-2">
            <code class="font-mono text-[12px] text-oc-peach">{p.name}</code>
            {#if p.required}<span class="ml-1 text-[10px] text-dark-text-muted">required</span>{/if}
          </td>
          {#if showType}
            <td class="whitespace-nowrap px-3 py-2 font-mono text-[11.5px] text-dark-text-muted">
              {p.type ?? ''}
            </td>
          {/if}
          <td class="px-3 py-2 leading-relaxed text-dark-text-secondary">{p.description}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>
