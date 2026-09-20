package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpguts"
)

var ErrTraceExportConflict = errors.New("trace export settings changed; reload before saving")

// TraceExportSettings belongs to exactly one workspace. Header values and the
// Langfuse secret are write-only in the management API (*** preserves a value).
type TraceExportSettings struct {
	Version        int64             `json:"version"`
	Enabled        bool              `json:"enabled"`
	Target         string            `json:"target"`
	Protocol       string            `json:"protocol"`
	Endpoint       string            `json:"endpoint"`
	Headers        map[string]string `json:"headers"`
	PublicKey      string            `json:"public_key"`
	SecretKey      string            `json:"secret_key"`
	IncludeContent bool              `json:"include_content"`
}

func DefaultTraceExportSettings() TraceExportSettings {
	return TraceExportSettings{Target: "collector", Protocol: "http", Headers: map[string]string{}}
}

func (c TraceExportSettings) Validate() error {
	if c.Target != "collector" && c.Target != "langfuse" {
		return fmt.Errorf("target must be collector or langfuse")
	}
	if c.Protocol != "http" && c.Protocol != "grpc" {
		return fmt.Errorf("protocol must be http or grpc")
	}
	if c.Target == "langfuse" && c.Protocol != "http" {
		return fmt.Errorf("Langfuse requires OTLP HTTP/protobuf")
	}
	if c.Version < 0 {
		return fmt.Errorf("invalid settings version")
	}
	if c.Endpoint != "" || c.Enabled {
		u, err := url.Parse(c.Endpoint)
		if err != nil || len(c.Endpoint) > 2048 || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("endpoint must be an http:// or https:// URL without credentials, query or fragment")
		}
		if c.Protocol == "grpc" && u.Path != "" && u.Path != "/" {
			return fmt.Errorf("gRPC endpoint must contain only scheme, host and port")
		}
	}
	if len(c.Headers) > 16 {
		return fmt.Errorf("at most 16 headers are allowed")
	}
	seen := map[string]bool{}
	for k, v := range c.Headers {
		key := strings.ToLower(k)
		if !httpguts.ValidHeaderFieldName(k) || !httpguts.ValidHeaderFieldValue(v) || len(k) > 128 || len(v) > 4096 || seen[key] || strings.HasPrefix(key, "grpc-") || strings.HasSuffix(key, "-bin") {
			return fmt.Errorf("invalid or duplicate header")
		}
		switch key {
		case "host", "content-type", "content-length", "connection", "transfer-encoding", "te":
			return fmt.Errorf("transport headers cannot be overridden")
		}
		if c.Target == "langfuse" && key == "authorization" {
			return fmt.Errorf("Langfuse authorization is generated from its keys")
		}
		seen[key] = true
	}
	if len(c.PublicKey) > 4096 || len(c.SecretKey) > 4096 || strings.Contains(c.PublicKey, ":") {
		return fmt.Errorf("invalid Langfuse keys")
	}
	if c.Enabled && c.Target == "langfuse" && (c.PublicKey == "" || c.SecretKey == "") {
		return fmt.Errorf("Langfuse public and secret keys are required")
	}
	return nil
}

func (c TraceExportSettings) Redacted() TraceExportSettings {
	h := make(map[string]string, len(c.Headers))
	for k := range c.Headers {
		h[k] = "***"
	}
	c.Headers = h
	if c.SecretKey != "" {
		c.SecretKey = "***"
	}
	return c
}

// Load is an internal lookup for the exporter. HTTP callers must authorize the
// selected workspace first. Save revalidates workspace.write in the store.
type TraceExportStorer interface {
	LoadTraceExportSettings(context.Context, string) (TraceExportSettings, error)
	SaveTraceExportSettings(context.Context, TraceExportSettings) (TraceExportSettings, error)
}
