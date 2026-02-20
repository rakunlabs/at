package config

import (
	"context"
	"fmt"
	"log/slog"

	_ "github.com/rakunlabs/chu/loader/loaderconsul"
	_ "github.com/rakunlabs/chu/loader/loadervault"
	"github.com/rakunlabs/logi"

	"github.com/rakunlabs/chu"
	"github.com/rakunlabs/tell"
)

var Service = ""

type Config struct {
	LogLevel string `cfg:"log_level" default:"info"`

	// Providers is a map of named provider configurations.
	// Each provider has a type ("anthropic", "openai", or "vertex"), along with
	// api_key, base_url, model, and extra_headers fields.
	//
	// Supported types:
	//   - "openai":     OpenAI and all OpenAI-compatible APIs (Groq, DeepSeek,
	//                   Mistral, Together AI, Fireworks, Perplexity, xAI/Grok,
	//                   OpenRouter, Ollama, LM Studio, vLLM, GitHub Models, etc.)
	//   - "anthropic":  Anthropic Claude API
	//   - "vertex":     Google Vertex AI (Gemini) via OpenAI-compatible endpoint
	//                   with automatic Google ADC authentication
	//
	// Example YAML:
	//
	//   providers:
	//     anthropic:
	//       type: anthropic
	//       api_key: "sk-ant-..."
	//       model: "claude-haiku-4-5"
	//     openai:
	//       type: openai
	//       api_key: "sk-..."
	//       model: "gpt-4o"
	//     groq:
	//       type: openai
	//       api_key: "gsk_..."
	//       base_url: "https://api.groq.com/openai/v1/chat/completions"
	//       model: "llama-3.3-70b-versatile"
	//     ollama:
	//       type: openai
	//       base_url: "http://localhost:11434/v1/chat/completions"
	//       model: "llama3.2"
	//     github:
	//       type: openai
	//       api_key: "ghp_..."
	//       base_url: "https://models.github.ai/inference/chat/completions"
	//       model: "openai/gpt-4.1"
	//       extra_headers:
	//         Accept: "application/vnd.github+json"
	//         X-GitHub-Api-Version: "2022-11-28"
	//     vertex:
	//       type: vertex
	//       base_url: "https://us-central1-aiplatform.googleapis.com/v1/projects/my-project/locations/us-central1/endpoints/openapi/chat/completions"
	//       model: "google/gemini-2.5-flash"
	Providers map[string]LLMConfig `cfg:"providers"`

	// Gateway configures the OpenAI-compatible gateway server.
	Gateway Gateway `cfg:"gateway"`

	Store     Store       `cfg:"store"`
	Server    Server      `cfg:"server"`
	Telemetry tell.Config `cfg:"telemetry"`
}

type Server struct {
	BasePath string `cfg:"base_path"`

	Port string `cfg:"port" default:"8080"`
	Host string `cfg:"host"`
}

// Gateway configures the OpenAI-compatible gateway server endpoints.
type Gateway struct {
	// AuthToken, if set, requires clients to send
	// "Authorization: Bearer <token>" to access the gateway endpoints.
	// If empty, no authentication is required.
	AuthToken string `cfg:"auth_token" log:"-"`
}

type Store struct {
	Postgres *StorePostgres `cfg:"postgres"`
}

type StorePostgres struct {
	TablePrefix  string  `cfg:"table_prefix"  default:"at_"`
	DBDatasource string  `cfg:"db_datasource" log:"-"`
	DBSchema     string  `cfg:"db_schema"`
	Migrate      Migrate `cfg:"migrate"`
}

type Migrate struct {
	DBDatasource string            `cfg:"db_datasource" log:"-"`
	DBSchema     string            `cfg:"db_schema"`
	DBTable      string            `cfg:"db_table"`
	Values       map[string]string `cfg:"values"`
}

// LLMConfig describes a single LLM provider configuration.
type LLMConfig struct {
	// Type is the provider type: "anthropic", "openai", or "vertex".
	// The "openai" type works with any OpenAI-compatible API.
	// The "vertex" type uses Google Application Default Credentials (ADC).
	Type string `cfg:"type"`

	// APIKey is the authentication key for the provider.
	// Optional for local providers like Ollama and for "vertex" type (uses ADC).
	APIKey string `cfg:"api_key" log:"-"`

	// BaseURL is the full endpoint URL for the provider's chat completions API.
	// For "openai" type, defaults to "https://api.openai.com/v1/chat/completions".
	// For "anthropic" type, defaults to "https://api.anthropic.com".
	// For "vertex" type, required. Format:
	//   https://{LOCATION}-aiplatform.googleapis.com/v1/projects/{PROJECT}/locations/{LOCATION}/endpoints/openapi/chat/completions
	BaseURL string `cfg:"base_url"`

	// Model is the default model identifier to use (e.g., "gpt-4o", "claude-haiku-4-5").
	Model string `cfg:"model"`

	// Models is the list of all models this provider supports.
	// When set, the gateway will reject requests for models not in this list (404).
	// The /v1/models endpoint will advertise all models in this list.
	// If empty, only the default Model is advertised and no strict validation is applied.
	Models []string `cfg:"models"`

	// ExtraHeaders allows setting additional HTTP headers sent with each request.
	// Useful for providers that require custom headers (e.g., GitHub Models).
	ExtraHeaders map[string]string `cfg:"extra_headers"`
}

func Load(ctx context.Context, path string) (*Config, error) {
	var cfg Config
	if err := chu.Load(ctx, path, &cfg); err != nil {
		return nil, err
	}

	if err := logi.SetLogLevel(cfg.LogLevel); err != nil {
		return nil, fmt.Errorf("set log level %s: %w", cfg.LogLevel, err)
	}

	slog.Info("loaded configuration", "config", chu.MarshalMap(cfg))

	return &cfg, nil
}
