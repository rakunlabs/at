# Provider compatibility updates — 2026-10-05

These changes affect adapter behavior and **new-provider presets**. They do not
rewrite providers, credentials, model lists, or defaults stored in the database.
Model access still depends on the upstream account; use **Fetch models** when
configuring a provider.

## Changes

- **Cerebras:** new preset using the existing OpenAI-compatible adapter at
  `https://api.cerebras.ai/v1/chat/completions`, with `gpt-oss-120b`.
- **Groq:** replace retired Llama 3.1, Llama 3.3 and Mixtral preset IDs with
  `openai/gpt-oss-120b` and `openai/gpt-oss-20b`.
- **DeepSeek:** seed `deepseek-flash` and `deepseek-v4-pro`, rather than the old
  chat/reasoner aliases. Their published efforts are `low`, `high`, `max`.
  Thinking can be configured through `extra_body.thinking.type`.
- **GPT-OSS:** recognize `low`, `medium`, `high` reasoning effort on the
  OpenAI-compatible path, including namespaced model IDs.
- **Cohere:** add `command-a-reasoning-08-2025` to the preset. Forward explicit
  thinking enable/disable and translate AT's `thinking.budget_tokens` into
  Cohere's `thinking.token_budget`. Zero retains the upstream default; disabled
  thinking sends no budget. Preserve thinking in responses and assistant
  tool-call history. Reasoning-effort-to-budget mapping is not implemented.
- **Anthropic:** honor explicit `adaptive` and `disabled` thinking. Adaptive
  thinking may also carry `output_config.effort`; explicit legacy `enabled`
  budgets retain their prior behavior. OAuth's automatic thinking configuration
  now uses adaptive mode for recognized adaptive Claude 4 models. Read
  `usage.output_tokens_details.thinking_tokens` in synchronous and streaming
  responses without adding it again to the inclusive output-token total.

## Official references

- [Cerebras OpenAI compatibility](https://inference-docs.cerebras.ai/resources/openai)
- [Cerebras model integration](https://inference-docs.cerebras.ai/integrations/huggingface)
- [Groq deprecations](https://console.groq.com/docs/deprecations)
- [Groq API reference](https://console.groq.com/docs/api-reference)
- [DeepSeek model list and effort tables](https://api-docs.deepseek.com/api/list-models/)
- [DeepSeek API usage](https://api-docs.deepseek.com/)
- [Cohere reasoning](https://docs.cohere.com/docs/reasoning)
- [Claude thinking controls and usage details](https://platform.claude.com/docs/en/build-with-claude/thinking-steering-and-cost)

Regression tests use local mock HTTP servers, not paid upstream calls. Live
model availability, account permissions and OAuth acceptance require verification
with the deployment's credentials.
