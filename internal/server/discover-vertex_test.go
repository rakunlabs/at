package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/service"
)

// TestDiscoverVertexModels exercises the whole discovery path: the stored key
// is exchanged for an access token, Model Garden is paged with it, and only the
// models each Vertex adapter can call are returned.
func TestDiscoverVertexModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
		case "/v1beta1/publishers/google/models":
			if got := r.Header.Get("Authorization"); got != "Bearer tok" {
				t.Errorf("Authorization = %q", got)
			}
			if r.URL.Query().Get("pageToken") == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"publisherModels": []any{
						map[string]any{"name": "publishers/google/models/gemini-2.5-flash"},
						map[string]any{"name": "publishers/google/models/imagen-4.0-generate-001"},
					},
					"nextPageToken": "p2",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"publisherModels": []any{
					map[string]any{"name": "publishers/google/models/gemini-2.5-pro"},
					map[string]any{"name": "publishers/google/models/gemini-embedding-001"},
					map[string]any{"name": "publishers/google/models/text-embedding-005"},
					map[string]any{"name": "publishers/google/models/gemini-2.5-flash"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cfg := config.LLMConfig{
		Type:            "vertex-gemini",
		BaseURL:         srv.URL,
		CredentialsJSON: fmt.Sprintf(`{"type":"authorized_user","client_id":"c","client_secret":"s","refresh_token":"r","token_uri":%q}`, srv.URL+"/token"),
	}

	chat, err := discoverVertexModels(context.Background(), cfg)
	if err != nil {
		t.Fatalf("discoverVertexModels: %v", err)
	}
	if want := []string{"gemini-2.5-flash", "gemini-2.5-pro"}; !slices.Equal(chat, want) {
		t.Fatalf("chat models = %v, want %v", chat, want)
	}

	embed, err := discoverVertexEmbeddingModels(context.Background(), cfg)
	if err != nil {
		t.Fatalf("discoverVertexEmbeddingModels: %v", err)
	}
	if want := []string{"gemini-embedding-001", "text-embedding-005"}; !slices.Equal(embed, want) {
		t.Fatalf("embedding models = %v, want %v", embed, want)
	}
}

func TestVertexDiscoveryHost(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.LLMConfig
		want string
	}{
		{name: "derived from region", cfg: config.LLMConfig{ExtraHeaders: map[string]string{"vertex_region": "europe-west4"}}, want: "https://europe-west4-aiplatform.googleapis.com"},
		{name: "default region", cfg: config.LLMConfig{}, want: "https://us-central1-aiplatform.googleapis.com"},
		{name: "full openai endpoint keeps only the host", cfg: config.LLMConfig{BaseURL: "https://asia-northeast1-aiplatform.googleapis.com/v1/projects/p/locations/asia-northeast1/endpoints/openapi/chat/completions"}, want: "https://asia-northeast1-aiplatform.googleapis.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := vertexDiscoveryHost(tt.cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("host = %q, want %q", got, tt.want)
			}
		})
	}
}

// The HTTP handlers must accept vertex-gemini for both chat and embedding
// discovery, reading the key the UI never sees back from the stored row.
func TestDiscoverVertexGeminiHTTP(t *testing.T) {
	var google *httptest.Server
	google = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "expires_in": 3600})
		case "/v1beta1/publishers/google/models":
			_ = json.NewEncoder(w).Encode(map[string]any{"publisherModels": []any{
				map[string]any{"name": "publishers/google/models/gemini-2.5-pro"},
				map[string]any{"name": "publishers/google/models/gemini-embedding-001"},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer google.Close()
	key := fmt.Sprintf(`{"type":"authorized_user","client_id":"c","client_secret":"s","refresh_token":"r","token_uri":%q}`, google.URL+"/token")

	f := newMachineFixture(t)
	create := httptest.NewRequest(http.MethodPost, "/api/v1/personal-providers", strings.NewReader(fmt.Sprintf(
		`{"key":"vg","config":{"type":"vertex-gemini","model":"gemini-2.5-pro","base_url":%q,"credentials_json":%q}}`, google.URL, key,
	))).WithContext(f.ctx)
	w := httptest.NewRecorder()
	f.s.CreatePersonalProviderAPI(w, create)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created service.ProviderRecord
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	for _, tc := range []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		want    string
	}{
		{"models", f.s.DiscoverPersonalProviderModelsAPI, "gemini-2.5-pro"},
		{"embeddings", f.s.DiscoverPersonalProviderEmbeddingModelsAPI, "gemini-embedding-001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// What the edit form sends: the redacted key, not the real one.
			body := fmt.Sprintf(`{"provider_id":%q,"config":{"type":"vertex-gemini","base_url":%q,"credentials_json":"***"}}`, created.ID, google.URL)
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)).WithContext(f.ctx)
			w := httptest.NewRecorder()
			tc.handler(w, r)
			if w.Code != http.StatusOK {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			var resp discoverResponse
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if !slices.Equal(resp.Models, []string{tc.want}) {
				t.Fatalf("models = %v", resp.Models)
			}
		})
	}
}
