package server

import (
	"encoding/json"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestSandboxBootstrapAndCapabilities(t *testing.T) {
	m, err := sandboxManager(nil)
	if err != nil || m.Driver().Name() != "docker" {
		t.Fatalf("default backend: %v", err)
	}
	for _, cfg := range []*service.Sandbox{{Backend: "unknown"}, {Backend: "kubernetes"}, {Kubernetes: &service.KubernetesSandbox{}}, {Backend: "kubernetes", Kubernetes: &service.KubernetesSandbox{}}} {
		if _, err := sandboxManager(cfg); err == nil {
			t.Fatalf("invalid backend configuration accepted: %+v", cfg)
		}
	}
	s := &Server{containerManager: m}
	data, err := json.Marshal(s.developerSpaceResponse(&service.DeveloperSpace{ID: "space"}))
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		ID      string `json:"id"`
		Runtime struct {
			Backend       string `json:"backend"`
			PreservesRoot bool   `json:"preserves_root_on_stop"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != "space" || response.Runtime.Backend != "docker" || !response.Runtime.PreservesRoot {
		t.Fatalf("response shape/capabilities changed: %s", data)
	}
}
