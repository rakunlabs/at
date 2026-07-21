package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rakunlabs/at/internal/config"
)

func TestInfoAPIIncludesBuildMetadata(t *testing.T) {
	s := &Server{
		config:    config.Server{Name: "AT Test"},
		providers: map[string]ProviderInfo{},
		storeType: "postgres",
		version:   "v1.2.3",
		commit:    "abc1234",
		buildDate: "2026-07-22T12:34:56Z",
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)

	s.InfoAPI(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response infoResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Version != "v1.2.3" || response.Commit != "abc1234" || response.BuildDate != "2026-07-22T12:34:56Z" {
		t.Fatalf("unexpected build metadata: version=%q commit=%q build_date=%q", response.Version, response.Commit, response.BuildDate)
	}
}
