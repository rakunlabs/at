package service

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation"
)

var ErrSystemSettingsConflict = errors.New("system settings changed; reload before saving")

// SystemSettings is installation configuration, never workspace configuration.
// Kubeconfig names an operator-provisioned file, not uploaded credential content.
type SystemSettings struct {
	Version           int64   `json:"version"`
	Name              string  `json:"name"`
	ExternalURL       string  `json:"external_url"`
	LogLevel          string  `json:"log_level"`
	WorkspaceTTLHours int     `json:"workspace_ttl_hours"`
	Sandbox           Sandbox `json:"sandbox"`
}

type Sandbox struct {
	Backend    string             `json:"backend"`
	Kubernetes *KubernetesSandbox `json:"kubernetes,omitempty"`
}

type KubernetesSandbox struct {
	Namespace             string   `json:"namespace"`
	DeploymentID          string   `json:"deployment_id"`
	Kubeconfig            string   `json:"kubeconfig"`
	HelperImage           string   `json:"helper_image"`
	StorageClass          string   `json:"storage_class"`
	HomeStorageClass      string   `json:"home_storage_class"`
	HomeAccessMode        string   `json:"home_access_mode"`
	HomeSize              string   `json:"home_size"`
	WorkspaceSize         string   `json:"workspace_size"`
	RuntimeClass          string   `json:"runtime_class"`
	PodPidsLimit          int      `json:"pod_pids_limit"`
	NetworkPolicyEnforced bool     `json:"network_policy_enforced"`
	SingleReplica         bool     `json:"single_replica"`
	BlockedCIDRs          []string `json:"blocked_cidrs"`
}

func DefaultSystemSettings() SystemSettings {
	return SystemSettings{Name: "AT", LogLevel: "info", WorkspaceTTLHours: 24, Sandbox: Sandbox{Backend: "docker"}}
}

func (s *SystemSettings) Normalize() error {
	s.Name = strings.TrimSpace(s.Name)
	s.ExternalURL = strings.TrimRight(strings.TrimSpace(s.ExternalURL), "/")
	s.LogLevel = strings.ToLower(strings.TrimSpace(s.LogLevel))
	if s.Version < 0 || s.Name == "" || len(s.Name) > 120 || strings.ContainsAny(s.Name, "\r\n") {
		return fmt.Errorf("a server name of 1–120 bytes and a valid version are required")
	}
	switch s.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log_level must be debug, info, warn or error")
	}
	if s.WorkspaceTTLHours < -1 || s.WorkspaceTTLHours > 87600 {
		return fmt.Errorf("workspace retention must be -1 (disabled) or 0–87600 hours; 0 uses 24 hours")
	}
	if s.ExternalURL != "" {
		u, err := url.Parse(s.ExternalURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
			return fmt.Errorf("public URL must be an HTTP(S) origin without credentials, query or fragment")
		}
		// BasePath is added by route builders, never stored twice in this URL.
		if u.Path != "" {
			return fmt.Errorf("public URL must not include a path; the deployment base path is added automatically")
		}
	}
	if s.Sandbox.Backend == "" {
		s.Sandbox.Backend = "docker"
	}
	switch s.Sandbox.Backend {
	case "docker":
		if s.Sandbox.Kubernetes != nil {
			return fmt.Errorf("Kubernetes settings require the Kubernetes backend")
		}
	case "kubernetes":
		if s.Sandbox.Kubernetes == nil {
			return fmt.Errorf("Kubernetes settings are required")
		}
		k, err := NormalizeKubernetesSandbox(*s.Sandbox.Kubernetes)
		if err != nil {
			return err
		}
		s.Sandbox.Kubernetes = &k
	default:
		return fmt.Errorf("unsupported sandbox backend %q", s.Sandbox.Backend)
	}
	return nil
}

// NormalizeKubernetesSandbox is shared by persistence and driver construction.
func NormalizeKubernetesSandbox(opts KubernetesSandbox) (KubernetesSandbox, error) {
	if !opts.SingleReplica {
		return opts, fmt.Errorf("Kubernetes sandboxes require one AT replica and single_replica: true")
	}
	if !opts.NetworkPolicyEnforced {
		return opts, fmt.Errorf("verify the cluster CNI enforces NetworkPolicy before enabling network_policy_enforced")
	}
	if opts.PodPidsLimit <= 0 {
		return opts, fmt.Errorf("declare the positive kubelet podPidsLimit configured on every sandbox node")
	}
	if len(validation.IsDNS1123Label(opts.Namespace)) != 0 || len(validation.IsDNS1123Label(opts.DeploymentID)) != 0 {
		return opts, fmt.Errorf("namespace and deployment_id must be non-empty DNS labels")
	}
	switch opts.Namespace {
	case "default", "kube-system", "kube-public", "kube-node-lease":
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
	if len(opts.Kubeconfig) > 4096 || strings.ContainsAny(opts.Kubeconfig, "\x00\r\n") {
		return opts, fmt.Errorf("kubeconfig must name a file on the AT host")
	}
	if opts.HomeAccessMode == "" {
		opts.HomeAccessMode = "ReadWriteMany"
	}
	if opts.HomeAccessMode != "ReadWriteMany" && opts.HomeAccessMode != "ReadWriteOnce" {
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
	if len(opts.BlockedCIDRs) > 256 {
		return opts, fmt.Errorf("at most 256 blocked CIDRs are allowed")
	}
	for _, cidr := range opts.BlockedCIDRs {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return opts, fmt.Errorf("invalid blocked CIDR %q: %w", cidr, err)
		}
	}
	return opts, nil
}

type SystemSettingsStorer interface {
	GetSystemSettings(context.Context) (*SystemSettings, error)
	SaveSystemSettings(context.Context, SystemSettings) (*SystemSettings, error)
}
