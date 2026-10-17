package service

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/rakunlabs/at/internal/config"
)

type ProviderUsePolicy struct {
	// Off by default. Blocks catalog-marked personal providers and known
	// catalog endpoints, conservatively including their paid accounts too.
	BlockFreeTier bool `json:"block_free_tier"`
	// nil = unrestricted; [] = no personal endpoints; exact hostnames only.
	AllowedPersonalHosts []string `json:"allowed_personal_hosts"`
	Version              int64    `json:"version"`
}

type ProviderUsePolicyStorer interface {
	GetProviderUsePolicy(context.Context) (*ProviderUsePolicy, error)
	SaveProviderUsePolicy(context.Context, ProviderUsePolicy) (*ProviderUsePolicy, error)
}

func (p ProviderUsePolicy) Validate() error {
	if len(p.AllowedPersonalHosts) > 100 {
		return fmt.Errorf("at most 100 personal provider hosts are allowed")
	}
	for _, host := range p.AllowedPersonalHosts {
		u, err := url.Parse("https://" + host)
		if err != nil || u.Hostname() != host || host != strings.ToLower(strings.TrimSpace(host)) || strings.ContainsAny(host, "/@?#*:") || host == "" {
			return fmt.Errorf("use exact lowercase hostnames without ports, paths or wildcards")
		}
	}
	return nil
}

func (p ProviderUsePolicy) Admits(cfg config.LLMConfig) bool {
	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return false
	}
	host := strings.TrimRight(strings.ToLower(u.Hostname()), ".")
	if host == "" && cfg.Type == "gemini" {
		host = "generativelanguage.googleapis.com"
	}
	if p.BlockFreeTier {
		if cfg.FreeTier != "" {
			return false
		}
		for _, entry := range FreeTierCatalog {
			known, _ := url.Parse(entry.URL)
			if host == known.Hostname() {
				return false
			}
		}
	}
	if p.AllowedPersonalHosts != nil {
		// A proxy could bypass endpoint policy, so require ordinary direct
		// HTTPS calls when the administrator uses this allowlist.
		return u.Scheme == "https" && u.User == nil && cfg.Proxy == "" && !cfg.InsecureSkipVerify && slices.Contains(p.AllowedPersonalHosts, host)
	}
	return true
}
