package server

import (
	"fmt"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/container"
	"github.com/rakunlabs/at/internal/service/sandboxkube"
)

func sandboxManager(cfg *service.Sandbox) (*container.Manager, error) {
	if cfg == nil || cfg.Backend == "" || cfg.Backend == "docker" {
		if cfg != nil && cfg.Kubernetes != nil {
			return nil, fmt.Errorf("sandbox.kubernetes requires backend: kubernetes")
		}
		return container.New(), nil
	}
	if cfg.Backend != "kubernetes" {
		return nil, fmt.Errorf("unsupported sandbox backend %q", cfg.Backend)
	}
	if cfg.Kubernetes == nil {
		return nil, fmt.Errorf("sandbox.kubernetes settings are required")
	}
	driver, err := sandboxkube.New(*cfg.Kubernetes)
	if err != nil {
		return nil, fmt.Errorf("configure Kubernetes sandboxes: %w", err)
	}
	return container.NewWithDriver(driver), nil
}

func (s *Server) developerSpaceResponse(space *service.DeveloperSpace) any {
	var runtime container.RuntimeCapabilities
	if s.containerManager != nil {
		runtime = s.containerManager.Capabilities()
	}
	return struct {
		*service.DeveloperSpace
		Runtime container.RuntimeCapabilities `json:"runtime"`
	}{space, runtime}
}
