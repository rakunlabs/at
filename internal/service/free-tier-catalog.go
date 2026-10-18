package service

// FreeTierEntry is setup documentation, not a balance, entitlement or promise
// of free service. Models/limits must be checked in the user's upstream account.
type FreeTierEntry struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	URL       string `json:"base_url"`
	Signup    string `json:"signup_url"`
	Source    string `json:"source_url"`
	Note      string `json:"note"`
	Privacy   string `json:"privacy"`
	Reviewed  string `json:"reviewed_at"`
	ModelHint string `json:"model_hint"`
}

// Official API endpoints only. No web cookies, scraped consumer interfaces,
// temporary signup credits disguised as recurring allowances, or remote feeds.
var FreeTierCatalog = []FreeTierEntry{
	{"groq", "Groq", "openai", "https://api.groq.com/openai/v1/chat/completions", "https://console.groq.com/keys", "https://console.groq.com/docs/rate-limits", "Free-plan limits are per organization and model. Copy your account's actual RPM/RPD/TPD limits; cached tokens may be excluded upstream.", "Review the API data policy before sending confidential content.", "2026-10-17", "openai/gpt-oss-20b"},
	{"google-ai", "Google AI Studio", "gemini", "https://generativelanguage.googleapis.com", "https://aistudio.google.com/apikey", "https://ai.google.dev/gemini-api/docs/pricing", "Only selected models have a free tier. Limits depend on project, model, region and account billing status.", "Free-tier content may be used to improve Google's products. Do not send confidential data without reviewing the terms.", "2026-10-17", ""},
	{"openrouter", "OpenRouter free models", "openai", "https://openrouter.ai/api/v1/chat/completions", "https://openrouter.ai/settings/keys", "https://openrouter.ai/docs/faq", "Use openrouter/free or an explicitly :free model. The free-model request pool is shared across models; paid models are separate.", "Requests go to downstream providers. Review your account privacy and model training settings.", "2026-10-17", "openrouter/free"},
	{"mistral", "Mistral Experiment", "openai", "https://api.mistral.ai/v1/chat/completions", "https://console.mistral.ai/api-keys/", "https://docs.mistral.ai/deployment/ai-studio/tier", "Check current Experiment eligibility and organization limits in the console. Do not assume a monthly token grant.", "Review Experiment data-use and production-use terms before use.", "", ""},
	{"nvidia", "NVIDIA API Catalog", "openai", "https://integrate.api.nvidia.com/v1/chat/completions", "https://build.nvidia.com/", "https://docs.api.nvidia.com/nim/docs", "Development/evaluation access depends on account and model. This is not a production capacity guarantee.", "Review NVIDIA and the chosen model's terms; evaluation access is not automatically approved for redistribution.", "", ""},
	{"sambanova", "SambaNova", "openai", "https://api.sambanova.ai/v1/chat/completions", "https://cloud.sambanova.ai/", "https://docs.sambanova.ai/cloud/docs/get-started/overview", "Check available free-plan models and current account limits in the console.", "Review free-plan data handling, commercial-use and sharing terms.", "", ""},
	{"huggingface", "Hugging Face Inference Providers", "openai", "https://router.huggingface.co/v1/chat/completions", "https://huggingface.co/settings/tokens", "https://huggingface.co/docs/inference-providers/pricing", "Monthly included credits, not a fixed token pool. Additional usage may be billed; disable paid usage upstream if required.", "Requests may go to third-party inference providers; review both policies.", "", ""},
}

func FreeTierByID(id string) (FreeTierEntry, bool) {
	for _, entry := range FreeTierCatalog {
		if entry.ID == id {
			return entry, true
		}
	}
	return FreeTierEntry{}, false
}
