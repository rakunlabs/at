<script lang="ts">
  // Dispatches the selected API reference section. Keeping the switch here
  // rather than in Docs.svelte lets the page stay a layout shell.
  import type { InfoProvider } from '@/lib/api/gateway';
  import type { MCPServer } from '@/lib/api/mcp-servers';
  import ApiOverview from './ApiOverview.svelte';
  import ApiEndpoints from './ApiEndpoints.svelte';
  import ApiProxy from './ApiProxy.svelte';
  import ApiAuthentication from './ApiAuthentication.svelte';
  import ApiCodeExamples from './ApiCodeExamples.svelte';
  import ApiOpencodeConfig from './ApiOpencodeConfig.svelte';
  import ApiMcpConfig from './ApiMcpConfig.svelte';
  import ApiClaudeMarketplace from './ApiClaudeMarketplace.svelte';
  import ApiListModels from './ApiListModels.svelte';
  import ApiModels from './ApiModels.svelte';

  interface Props {
    sectionId: string;
    baseUrl: string;
    instanceName: string;
    providers: InfoProvider[];
    mcpServers: MCPServer[];
    models: string[];
    exampleModel: string;
    loading?: boolean;
    infoError?: string;
    onretry?: () => void;
    codeTab?: string;
    mcpName?: string;
  }

  let {
    sectionId,
    baseUrl,
    instanceName,
    providers,
    mcpServers,
    models,
    exampleModel,
    loading = false,
    infoError = '',
    onretry,
    codeTab = $bindable('python'),
    mcpName = $bindable(''),
  }: Props = $props();
</script>

{#if sectionId === 'overview'}
  <ApiOverview {baseUrl} />
{:else if sectionId === 'endpoints'}
  <ApiEndpoints {baseUrl} />
{:else if sectionId === 'proxy'}
  <ApiProxy {baseUrl} />
{:else if sectionId === 'authentication'}
  <ApiAuthentication />
{:else if sectionId === 'code-examples'}
  <ApiCodeExamples {baseUrl} model={exampleModel} bind:activeTab={codeTab} />
{:else if sectionId === 'opencode-config'}
  <ApiOpencodeConfig {baseUrl} {instanceName} {providers} {loading} />
{:else if sectionId === 'mcp-configuration'}
  <ApiMcpConfig {baseUrl} servers={mcpServers} bind:selectedName={mcpName} />
{:else if sectionId === 'claude-marketplace'}
  <ApiClaudeMarketplace {baseUrl} />
{:else if sectionId === 'list-models'}
  <ApiListModels {baseUrl} />
{:else if sectionId === 'available-models'}
  <ApiModels {models} {loading} error={infoError} {onretry} />
{/if}
