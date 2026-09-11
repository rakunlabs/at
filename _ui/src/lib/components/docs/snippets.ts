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

  const providerId = (opts.instanceName || 'at').toLowerCase().replace(/\s+/g, '-');
  return JSON.stringify(
    {
      $schema: 'https://opencode.ai/config.json',
      provider: {
        [providerId]: {
          npm: '@ai-sdk/openai-compatible',
          name: opts.instanceName || 'AT',
          options: { baseURL: `${opts.baseUrl}/gateway/v1` },
          models: modelsObj,
        },
      },
    },
    null,
    2,
  );
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
