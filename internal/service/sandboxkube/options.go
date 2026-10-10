// Package sandboxkube implements Kubernetes sandboxes. It currently requires a
// single AT replica; pod names alone are not distributed lifecycle ownership.
package sandboxkube

import (
	"fmt"
	"net/netip"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/rakunlabs/at/internal/config"
	"github.com/rakunlabs/at/internal/sandboxruntime"
	"github.com/rakunlabs/at/internal/service/container"
)

func normalizeOptions(opts config.KubernetesSandbox) (config.KubernetesSandbox, error) {
	if !opts.SingleReplica {
		return opts, fmt.Errorf("Kubernetes sandboxes currently require single_replica: true and one AT replica")
	}
	if !opts.NetworkPolicyEnforced {
		return opts, fmt.Errorf("Kubernetes sandboxes require network_policy_enforced: true after verifying the cluster CNI enforces NetworkPolicy")
	}
	if opts.PodPidsLimit <= 0 {
		return opts, fmt.Errorf("pod_pids_limit must declare the positive kubelet podPidsLimit configured on every sandbox node")
	}
	if len(validation.IsDNS1123Label(opts.Namespace)) != 0 || len(validation.IsDNS1123Label(opts.DeploymentID)) != 0 {
		return opts, fmt.Errorf("namespace and deployment_id must be non-empty DNS labels")
	}
	if opts.Namespace == "default" || opts.Namespace == "kube-system" || opts.Namespace == "kube-public" || opts.Namespace == "kube-node-lease" {
		return opts, fmt.Errorf("use a dedicated sandbox namespace, not %q", opts.Namespace)
	}
	if opts.HelperImage == "" || strings.HasPrefix(opts.HelperImage, "-") || strings.ContainsAny(opts.HelperImage, " \t\r\n") {
		return opts, fmt.Errorf("helper_image must name the operator-built at-sandbox helper image")
	}
	if opts.RuntimeClass != "" && len(validation.IsDNS1123Subdomain(opts.RuntimeClass)) != 0 {
		return opts, fmt.Errorf("invalid runtime_class")
	}
	for _, class := range []string{opts.StorageClass, opts.HomeStorageClass} {
		if class != "" && len(validation.IsDNS1123Subdomain(class)) != 0 {
			return opts, fmt.Errorf("invalid storage class")
		}
	}
	if opts.HomeAccessMode == "" {
		opts.HomeAccessMode = string(corev1.ReadWriteMany)
	}
	if opts.HomeAccessMode != string(corev1.ReadWriteMany) && opts.HomeAccessMode != string(corev1.ReadWriteOnce) {
		return opts, fmt.Errorf("home_access_mode must be ReadWriteMany or ReadWriteOnce")
	}
	if opts.HomeSize == "" {
		opts.HomeSize = "5Gi"
	}
	if opts.WorkspaceSize == "" {
		opts.WorkspaceSize = "20Gi"
	}
	for _, size := range []string{opts.HomeSize, opts.WorkspaceSize} {
		q, err := resource.ParseQuantity(size)
		if err != nil || q.Sign() <= 0 {
			return opts, fmt.Errorf("home_size and workspace_size must be positive Kubernetes quantities")
		}
	}
	for _, cidr := range opts.BlockedCIDRs {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return opts, fmt.Errorf("invalid blocked CIDR %q: %w", cidr, err)
		}
	}
	return opts, nil
}

func validateSandbox(cfg container.Config, opts config.KubernetesSandbox) error {
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
