<script lang="ts">
  // Dispatches the selected reference section. Keeping the switch here
  // rather than in Docs.svelte lets the page stay a layout shell.
  import type { InfoProvider } from '@/lib/api/gateway';
  import type { MCPServer } from '@/lib/api/mcp-servers';
  import ApiOverview from './ApiOverview.svelte';
  import ApiQuickstart from './ApiQuickstart.svelte';
  import ApiAuthentication from './ApiAuthentication.svelte';
  import ApiModels from './ApiModels.svelte';
  import ApiEndpoints from './ApiEndpoints.svelte';
  import ApiChat from './ApiChat.svelte';
  import ApiAnthropic from './ApiAnthropic.svelte';
  import ApiRouting from './ApiRouting.svelte';
  import ApiEmbeddings from './ApiEmbeddings.svelte';
  import ApiMedia from './ApiMedia.svelte';
  import ApiDecisions from './ApiDecisions.svelte';
  import ApiProxy from './ApiProxy.svelte';
  import ApiTracing from './ApiTracing.svelte';
  import ApiErrors from './ApiErrors.svelte';
  import ApiOpencodeConfig from './ApiOpencodeConfig.svelte';
  import ApiMcpConfig from './ApiMcpConfig.svelte';
  import ApiClaudeMarketplace from './ApiClaudeMarketplace.svelte';

  interface Props {
    sectionId: string;
    baseUrl: string;
    instanceName: string;
    providers: InfoProvider[];
    mcpServers: MCPServer[];
    models: string[];
    exampleModel: string;
    fallbackModel: string;
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
    fallbackModel,
    loading = false,
    infoError = '',
    onretry,
    codeTab = $bindable('python'),
    mcpName = $bindable(''),
  }: Props = $props();
</script>

{#if sectionId === 'overview'}
  <ApiOverview {baseUrl} modelCount={models.length} providerCount={providers.length} {loading} />
{:else if sectionId === 'quickstart'}
  <ApiQuickstart {baseUrl} model={exampleModel} bind:activeTab={codeTab} />
{:else if sectionId === 'authentication'}
  <ApiAuthentication />
{:else if sectionId === 'available-models'}
  <ApiModels {baseUrl} {providers} {loading} error={infoError} {onretry} />
{:else if sectionId === 'endpoints'}
  <ApiEndpoints {baseUrl} />
{:else if sectionId === 'chat-completions'}
  <ApiChat {baseUrl} model={exampleModel} {fallbackModel} />
{:else if sectionId === 'anthropic-messages'}
  <ApiAnthropic {baseUrl} model={exampleModel} />
{:else if sectionId === 'routing'}
  <ApiRouting model={exampleModel} {fallbackModel} />
{:else if sectionId === 'embeddings'}
  <ApiEmbeddings {baseUrl} model={exampleModel} />
{:else if sectionId === 'media'}
  <ApiMedia {baseUrl} />
{:else if sectionId === 'decisions'}
  <ApiDecisions {baseUrl} />
{:else if sectionId === 'proxy'}
  <ApiProxy {baseUrl} />
{:else if sectionId === 'tracing'}
  <ApiTracing {baseUrl} model={exampleModel} />
{:else if sectionId === 'errors'}
  <ApiErrors />
{:else if sectionId === 'opencode-config'}
  <ApiOpencodeConfig {baseUrl} {instanceName} {providers} {loading} />
{:else if sectionId === 'mcp-configuration'}
  <ApiMcpConfig {baseUrl} servers={mcpServers} bind:selectedName={mcpName} />
{:else if sectionId === 'claude-marketplace'}
  <ApiClaudeMarketplace {baseUrl} />
{/if}
