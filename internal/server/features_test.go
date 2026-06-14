package server

import (
	"net/http"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestFeatureKeyForAPIRequest(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		method   string
		basePath string
		want     string
	}{
		{
			name:   "provider list remains available",
			path:   "/api/v1/providers",
			method: http.MethodGet,
		},
		{
			name:   "provider create uses setup feature",
			path:   "/api/v1/providers",
			method: http.MethodPost,
			want:   service.FeatureProviderSetup,
		},
		{
			name:   "provider auth status uses setup feature",
			path:   "/api/v1/providers/device-auth-status",
			method: http.MethodGet,
			want:   service.FeatureProviderSetup,
		},
		{
			name:   "model pricing uses provider setup feature",
			path:   "/api/v1/model-pricing/catalog",
			method: http.MethodGet,
			want:   service.FeatureProviderSetup,
		},
		{
			name:   "chat sessions use chat workbench",
			path:   "/api/v1/chat/sessions/abc/messages",
			method: http.MethodPost,
			want:   service.FeatureChatWorkbench,
		},
		{
			name:   "admin chat uses chat workbench",
			path:   "/api/v1/chat/completions",
			method: http.MethodPost,
			want:   service.FeatureChatWorkbench,
		},
		{
			name:   "agents use agents feature",
			path:   "/api/v1/agents/agent-id/runtime-state",
			method: http.MethodPut,
			want:   service.FeatureAgents,
		},
		{
			name:   "workflows use automation feature",
			path:   "/api/v1/workflows/run/workflow-id",
			method: http.MethodPost,
			want:   service.FeatureAutomation,
		},
		{
			name:   "triggers use automation feature",
			path:   "/api/v1/triggers/trigger-id",
			method: http.MethodPut,
			want:   service.FeatureAutomation,
		},
		{
			name:   "bots use chat feature",
			path:   "/api/v1/bots/bot-id/start",
			method: http.MethodPost,
			want:   service.FeatureChatWorkbench,
		},
		{
			name:   "rag uses rag feature",
			path:   "/api/v1/rag/collections",
			method: http.MethodGet,
			want:   service.FeatureRAG,
		},
		{
			name:   "files use files feature",
			path:   "/api/v1/files/browse",
			method: http.MethodGet,
			want:   service.FeatureFiles,
		},
		{
			name:   "connections use connections feature",
			path:   "/api/v1/connections",
			method: http.MethodGet,
			want:   service.FeatureConnections,
		},
		{
			name:   "integration packs use connections feature",
			path:   "/api/v1/integration-packs/foo/install",
			method: http.MethodPost,
			want:   service.FeatureConnections,
		},
		{
			name:   "node configs use automation feature",
			path:   "/api/v1/node-configs/config-id",
			method: http.MethodDelete,
			want:   service.FeatureAutomation,
		},
		{
			name:   "public webhooks use automation feature",
			path:   "/webhooks/my-hook",
			method: http.MethodPost,
			want:   service.FeatureAutomation,
		},
		{
			name:   "organizations use organization feature",
			path:   "/api/v1/organizations/org-id/agents",
			method: http.MethodPost,
			want:   service.FeatureOrganizationWorkflows,
		},
		{
			name:   "tasks use organization feature",
			path:   "/api/v1/tasks/task-id/process",
			method: http.MethodPost,
			want:   service.FeatureOrganizationWorkflows,
		},
		{
			name:     "base path is ignored",
			path:     "/at/api/v1/chat/sessions",
			method:   http.MethodGet,
			basePath: "/at",
			want:     service.FeatureChatWorkbench,
		},
		{
			name:   "features endpoint remains available",
			path:   "/api/v1/features",
			method: http.MethodGet,
		},
		{
			name:   "options preflight remains available",
			path:   "/api/v1/chat/sessions",
			method: http.MethodOptions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := featureKeyForAPIRequest(tt.path, tt.method, tt.basePath); got != tt.want {
				t.Fatalf("featureKeyForAPIRequest() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFeatureKeysForAPIRequest(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		method string
		want   []string
	}{
		{
			name:   "rag triggers require automation and rag",
			path:   "/api/v1/rag/collections/collection-id/triggers",
			method: http.MethodPost,
			want:   []string{service.FeatureAutomation, service.FeatureRAG},
		},
		{
			name:   "rag chat tools require chat and rag",
			path:   "/api/v1/mcp/rag-tools",
			method: http.MethodGet,
			want:   []string{service.FeatureChatWorkbench, service.FeatureRAG},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := featureKeysForAPIRequest(tt.path, tt.method, "")
			if len(got) != len(tt.want) {
				t.Fatalf("featureKeysForAPIRequest() = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("featureKeysForAPIRequest() = %v, want %v", got, tt.want)
				}
			}
		})
	}
}
