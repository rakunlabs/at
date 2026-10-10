// Package sandboxkube implements Kubernetes sandboxes. It currently requires a
// single AT replica; pod names alone are not distributed lifecycle ownership.
package sandboxkube

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/rakunlabs/at/internal/sandboxruntime"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/container"
)

func normalizeOptions(opts service.KubernetesSandbox) (service.KubernetesSandbox, error) {
	return service.NormalizeKubernetesSandbox(opts)
}

func validateSandbox(cfg container.Config, opts service.KubernetesSandbox) error {
	if cfg.Image == "" || strings.HasPrefix(cfg.Image, "-") || strings.ContainsAny(cfg.Image, " \t\r\n") {
		return fmt.Errorf("invalid sandbox image")
	}
	if cfg.PidsLimit > 0 && opts.PodPidsLimit > cfg.PidsLimit {
		return fmt.Errorf("kubelet podPidsLimit %d exceeds the requested PID ceiling %d", opts.PodPidsLimit, cfg.PidsLimit)
	}
	if cfg.HomeScope != "" && !container.ValidHomePath(cfg.HomePath) {
		return fmt.Errorf("invalid persistent home path")
	}
	if cfg.HomeScope != "" && (cfg.HomePath == sandboxruntime.Directory || strings.HasPrefix(cfg.HomePath, sandboxruntime.Directory+"/") || strings.HasPrefix(sandboxruntime.Directory, cfg.HomePath+"/")) {
		return fmt.Errorf("persistent home must not overlap the sandbox helper directory")
	}
	allowed := map[string]bool{}
	for _, cap := range container.PackageManagerCapabilities {
		allowed[cap] = true
	}
	for _, cap := range cfg.CapAdd {
		if !allowed[cap] {
			return fmt.Errorf("unsupported sandbox capability %q", cap)
		}
	}
	if cfg.DiskLimitBytes < 0 {
		return fmt.Errorf("disk limit must not be negative")
	}
	return nil
}

// memoryQuantity accepts the Docker-style suffix used by existing spaces.
func memoryQuantity(raw string) (resource.Quantity, error) {
	if raw == "" {
		raw = "4Gi"
	}
	for suffix, replacement := range map[string]string{"k": "Ki", "m": "Mi", "g": "Gi", "t": "Ti"} {
		if strings.HasSuffix(strings.ToLower(raw), suffix) {
			raw = raw[:len(raw)-1] + replacement
			break
		}
	}
	q, err := resource.ParseQuantity(raw)
	if err != nil || q.Sign() <= 0 {
		return resource.Quantity{}, fmt.Errorf("invalid memory limit %q", raw)
	}
	return q, nil
}
