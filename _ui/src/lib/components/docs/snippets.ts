// Code snippets rendered by the API reference sections. Pure string builders so
// the section components stay markup-only and the snippets stay diffable.

export interface CodeExampleTab {
  id: string;
  label: string;
  lang: string;
}

export const codeExampleTabs: CodeExampleTab[] = [
  { id: 'python', label: 'Python', lang: 'python' },
  { id: 'js', label: 'JavaScript', lang: 'javascript' },
  { id: 'go', label: 'Go', lang: 'go' },
  { id: 'curl', label: 'curl', lang: 'bash' },
];

export function pythonExample(model: string, url: string): string {
  return `from openai import OpenAI

client = OpenAI(
    base_url="${url}/gateway/v1",
    api_key="at_your_token_here",
)

response = client.chat.completions.create(
    model="${model}",
    messages=[
        {"role": "user", "content": "Hello!"}
    ],
)

print(response.choices[0].message.content)`;
}

export function jsExample(model: string, url: string): string {
  return `import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "${url}/gateway/v1",
  apiKey: "at_your_token_here",
});

const response = await client.chat.completions.create({
  model: "${model}",
  messages: [
    { role: "user", content: "Hello!" }
  ],
});

console.log(response.choices[0].message.content);`;
}

export function goExample(model: string, url: string): string {
  return `package main

import (
	"context"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func main() {
	client := openai.NewClient(
		option.WithBaseURL("${url}/gateway/v1"),
		option.WithAPIKey("at_your_token_here"),
	)

	resp, err := client.Chat.Completions.New(context.TODO(),
		openai.ChatCompletionNewParams{
			Model: "${model}",
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Hello!"),
			},
		},
	)
	if err != nil {
		panic(err)
	}

	fmt.Println(resp.Choices[0].Message.Content)
}`;
}

export function curlExample(model: string, url: string): string {
  return `curl ${url}/gateway/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer at_your_token_here" \\
  -d '{
    "model": "${model}",
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'`;
}

export function curlModelsExample(url: string): string {
  return `curl ${url}/gateway/v1/models \\
  -H "Authorization: Bearer at_your_token_here"`;
}

export function codeExampleFor(tab: string, model: string, url: string): string {
  switch (tab) {
    case 'python':
      return pythonExample(model, url);
    case 'js':
      return jsExample(model, url);
    case 'go':
      return goExample(model, url);
    case 'curl':
      return curlExample(model, url);
    default:
      return '';
  }
}

/** Provider id used as the key of the opencode `provider` block. */
function opencodeProviderId(instanceName: string): string {
  return (instanceName || 'at').toLowerCase().replace(/\s+/g, '-');
}

/**
 * Origin-absolute path of a `/gateway/v1/...` endpoint for this deployment.
 * The discovery plugin resolves `endpoint` and `modelInfoEndpoint` against the
 * origin, not against `baseURL`, so a prefix deployment (`https://host/at`)
 * must keep its prefix.
 */
function gatewayPath(baseUrl: string, endpoint: string): string {
  let path = '';
  try {
    path = new URL(baseUrl).pathname;
  } catch {
    const m = baseUrl.match(/^[a-z][a-z0-9+.-]*:\/\/[^/]+(\/.*)$/i);
    path = m ? m[1] : '';
  }
  return `${path.replace(/\/+$/, '')}/gateway/v1/${endpoint}`;
}

/**
 * opencode V2 config schema: `providers` / `package` / `settings`, with
 * `plugins` as `{package, options}` objects.
 */
const opencodeProviderPackage = '@opencode/ai/providers/openai-compatible';
const opencodeDiscoveryPlugin = 'opencode-models-discovery@1.9.0';

function opencodeConfig(opts: {
  instanceName: string;
  settings: Record<string, unknown>;
  models: Record<string, { name: string }>;
  discovery: boolean;
}): string {
  const id = opencodeProviderId(opts.instanceName);
  const name = opts.instanceName || 'AT';
  const cfg: Record<string, unknown> = { $schema: 'https://opencode.ai/config.json' };

  if (opts.discovery) {
    cfg.plugins = [{ package: opencodeDiscoveryPlugin, options: {} }];
  }
  cfg.providers = {
    [id]: {
      name,
      package: opencodeProviderPackage,
      settings: opts.settings,
      models: opts.models,
    },
  };

  return JSON.stringify(cfg, null, 2);
}

/**
 * `~/.config/opencode/opencode.json` provider block that lets the
 * `opencode-models-discovery` plugin read the model list from
 * `/gateway/v1/models` instead of pinning it in the file.
 */
export function opencodeDiscoveryConfig(opts: { baseUrl: string; instanceName: string }): string {
  return opencodeConfig({
    instanceName: opts.instanceName,
    settings: {
      baseURL: `${opts.baseUrl}/gateway/v1`,
      modelsDiscovery: {
        enabled: true,
        endpoint: gatewayPath(opts.baseUrl, 'models'),
        smartModelName: true,
        modelInfoFormat: 'litellm',
        modelInfoEndpoint: gatewayPath(opts.baseUrl, 'model/info'),
        cache: {
          enabled: true,
          ttlSeconds: 86400,
        },
      },
    },
    models: {},
    discovery: true,
  });
}

/**
 * `~/.config/opencode/opencode.json` provider block pointing at this gateway.
 * `models` are full ids in `provider/model` form.
 */
export function opencodeProviderConfig(opts: {
  baseUrl: string;
  instanceName: string;
  models: string[];
}): string {
  const modelsObj: Record<string, { name: string }> = {};
  for (const m of [...opts.models].sort()) {
    modelsObj[m] = { name: m };
  }

  return opencodeConfig({
    instanceName: opts.instanceName,
    settings: { baseURL: `${opts.baseUrl}/gateway/v1` },
    models: modelsObj,
    discovery: false,
  });
}

/**
 * `mcp` entry for opencode using the canonical "remote" transport. Private
 * servers carry an Authorization header; public ones omit it.
 */
export function opencodeMcpConfig(opts: {
  baseUrl: string;
  serverName: string;
  isPublic: boolean;
}): string {
  // Fall back to the builtin "management" server so the example is always
  // meaningful even before the user configures anything.
  const name = opts.serverName || 'management';
  const server: {
    type: string;
    url: string;
    enabled: boolean;
    headers?: Record<string, string>;
  } = {
    type: 'remote',
    url: `${opts.baseUrl}/gateway/v1/mcp/${name}`,
    enabled: true,
  };
  if (!opts.isPublic) {
    server.headers = { Authorization: 'Bearer at_xxxxx' };
  }
  return JSON.stringify(
    {
      $schema: 'https://opencode.ai/config.json',
      mcp: { [`at-${name}`]: server },
    },
    null,
    2,
  );
}

/** Streaming chat request with the opt-in AT extensions spelled out. */
export function curlChatExtensionsExample(url: string, model: string, fallback: string): string {
  return `curl -N ${url}/gateway/v1/chat/completions \\
  -H "Authorization: Bearer at_your_token_here" \\
  -H "Content-Type: application/json" \\
  -H "Idempotency-Key: 6f1c7a2e-retry-safe" \\
  -d '${JSON.stringify(
    {
      model,
      stream: true,
      stream_options: { include_usage: true },
      messages: [{ role: 'user', content: 'Summarise the release notes.' }],
      at_fallbacks: [fallback],
      timeout_ms: 60000,
    },
    null,
    2,
  )}'`;
}

export function curlResponsesExample(url: string, model: string): string {
  return `curl ${url}/gateway/v1/responses \\
  -H "Authorization: Bearer at_your_token_here" \\
  -H "Content-Type: application/json" \\
  -d '${JSON.stringify(
    {
      model,
      instructions: 'Answer in one sentence.',
      input: 'What is an LLM gateway?',
    },
    null,
    2,
  )}'`;
}

/** Environment for Claude Code (and other Anthropic-native clients). */
export function claudeCodeEnv(url: string, model: string): string {
  return `export ANTHROPIC_BASE_URL="${url}/gateway"
export ANTHROPIC_AUTH_TOKEN="at_your_token_here"
# A bare model name resolves through a routing profile of the same name;
# a provider/model id is used as-is.
export ANTHROPIC_MODEL="${model}"

claude`;
}

export function anthropicPythonExample(url: string, model: string): string {
  return `import anthropic

client = anthropic.Anthropic(
    base_url="${url}/gateway",
    api_key="at_your_token_here",
)

message = client.messages.create(
    model="${model}",
    max_tokens=1024,
    messages=[{"role": "user", "content": "Hello!"}],
)

print(message.content[0].text)`;
}

export function curlImageExample(url: string): string {
  return `curl ${url}/gateway/v1/images/generations \\
  -H "Authorization: Bearer at_your_token_here" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "openai/gpt-image-1",
    "prompt": "A red fox in fresh snow, watercolor",
    "size": "1024x1024"
  }'`;
}

export function curlSpeechExample(url: string): string {
  return `curl ${url}/gateway/v1/audio/speech \\
  -H "Authorization: Bearer at_your_token_here" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "openai/tts-1", "input": "Hello from AT.", "voice": "alloy"}' \\
  -o hello.mp3`;
}

export function curlTranscriptionExample(url: string): string {
  return `curl ${url}/gateway/v1/audio/transcriptions \\
  -H "Authorization: Bearer at_your_token_here" \\
  -F file=@meeting.m4a \\
  -F model=openai/whisper-1 \\
  -F response_format=text`;
}

export function curlModerationExample(url: string): string {
  return `curl ${url}/gateway/v1/moderations \\
  -H "Authorization: Bearer at_your_token_here" \\
  -H "Content-Type: application/json" \\
  -d '{"model": "openai/omni-moderation-latest", "input": ["text to classify"]}'`;
}

export function curlRerankExample(url: string): string {
  return `curl ${url}/gateway/v1/rerank \\
  -H "Authorization: Bearer at_your_token_here" \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "cohere/rerank-v3.5",
    "query": "How do I rotate a token?",
    "documents": ["Tokens can be paused.", "Rotate replaces the secret in place."],
    "top_n": 1
  }'`;
}

export function curlDecisionExample(url: string): string {
  return `curl ${url}/gateway/v1/decisions \\
  -H "Authorization: Bearer at_your_token_here" \\
  -H "Content-Type: application/json" \\
  -d '${JSON.stringify(
    {
      model: 'laya/auto',
      state: 'My invoice was charged twice this month.',
      questions: {
        department: {
          type: 'choice',
          instructions: 'Which team should handle this ticket?',
          criteria: { billing: 'Payments and invoices', support: 'Product problems' },
        },
        urgent: { type: 'noul', instructions: 'The customer needs an answer today.' },
      },
    },
    null,
    2,
  )}'`;
}

export function curlProxyExample(url: string): string {
  return `curl ${url}/gateway/v1/providers/gemini/v1beta/models \\
  -H "Authorization: Bearer at_your_token_here"`;
}

export function curlTraceLabelsExample(url: string, model: string): string {
  return `curl ${url}/gateway/v1/chat/completions \\
  -H "Authorization: Bearer at_your_token_here" \\
  -H "Content-Type: application/json" \\
  -H "x-at-session-id: support-ticket-4812" \\
  -H "x-at-trace-name: triage" \\
  -H "x-at-tags: support,beta" \\
  -H "x-at-environment: production" \\
  -H "x-at-user: customer-77" \\
  -d '{"model": "${model}", "messages": [{"role": "user", "content": "Hi"}]}'`;
}

export function curlScoreExample(url: string): string {
  return `curl ${url}/gateway/v1/scores \\
  -H "Authorization: Bearer at_your_token_here" \\
  -H "Content-Type: application/json" \\
  -d '{"trace_id": "<x-at-trace-id>", "name": "helpful", "data_type": "boolean", "bool_value": true}'`;
}

export const errorEnvelopeExample = `{
  "error": {
    "message": "model \\"openai/gpt-5x\\" not found",
    "type": "invalid_request_error",
    "param": "model",
    "code": "model_not_found"
  }
}`;

export function claudeMarketplaceCommands(jsonUrl: string, zipUrl: string): string {
  return `# Inside Claude Code:
/plugin marketplace add ${jsonUrl}
/plugin install <plugin-name>@at-mcp-servers
/reload-plugins

# Optional offline export:
curl -L ${zipUrl} -o at-claude-marketplace.zip
unzip at-claude-marketplace.zip -d at-claude-marketplace
/plugin marketplace add ./at-claude-marketplace`;
}
