package server

import (
	"context"
	"embed"
	"encoding/json"
	"log/slog"
	"sort"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

//go:embed connectors/*.json
var connectorFS embed.FS

// loadConnectors reads all embedded connector definitions into memory. These
// are the built-in connection TYPES (google, youtube, github, …) that replace
// the formerly hardcoded oauthProviders map. User-defined connectors and
// overrides live in the connectors table and are merged in at read time.
func (s *Server) loadConnectors() {
	entries, err := connectorFS.ReadDir("connectors")
	if err != nil {
		slog.Warn("failed to read connectors dir", "error", err)
		return
	}

	var loaded []service.Connector
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := connectorFS.ReadFile("connectors/" + entry.Name())
		if err != nil {
			slog.Warn("failed to read connector definition", "file", entry.Name(), "error", err)
			continue
		}
		var c service.Connector
		if err := json.Unmarshal(data, &c); err != nil {
			slog.Warn("failed to parse connector definition", "file", entry.Name(), "error", err)
			continue
		}
		if c.Slug == "" {
			slog.Warn("connector definition missing slug", "file", entry.Name())
			continue
		}
		c.Builtin = true
		loaded = append(loaded, c)
	}

	s.builtinConnectors = loaded
	slog.Info("loaded built-in connectors", "count", len(loaded))
}

// listConnectors returns the merged connector catalog: built-in definitions
// overlaid with user-defined / override rows from the store. A DB row with the
// same slug replaces the built-in (and is reported with Builtin=false).
func (s *Server) listConnectors(ctx context.Context) ([]service.Connector, error) {
	bySlug := make(map[string]service.Connector, len(s.builtinConnectors))
	order := make([]string, 0, len(s.builtinConnectors))
	for _, c := range s.builtinConnectors {
		bySlug[c.Slug] = c
		order = append(order, c.Slug)
	}

	if s.connectorStore != nil {
		res, err := s.connectorStore.ListConnectors(ctx, nil)
		if err != nil {
			return nil, err
		}
		if res != nil {
			for _, c := range res.Data {
				c.Builtin = false
				if _, exists := bySlug[c.Slug]; !exists {
					order = append(order, c.Slug)
				}
				bySlug[c.Slug] = c
			}
		}
	}

	out := make([]service.Connector, 0, len(bySlug))
	for _, slug := range order {
		out = append(out, bySlug[slug])
	}
	sort.Slice(out, func(i, j int) bool {
		ni, nj := out[i].Name, out[j].Name
		if ni == "" {
			ni = out[i].Slug
		}
		if nj == "" {
			nj = out[j].Slug
		}
		return strings.ToLower(ni) < strings.ToLower(nj)
	})
	return out, nil
}

// resolveConnector returns the effective connector for a slug: a user-defined
// / override row from the store wins; otherwise the built-in is returned.
// Returns (nil, nil) when no connector matches.
func (s *Server) resolveConnector(ctx context.Context, slug string) (*service.Connector, error) {
	if slug == "" {
		return nil, nil
	}
	if s.connectorStore != nil {
		row, err := s.connectorStore.GetConnector(ctx, slug)
		if err != nil {
			return nil, err
		}
		if row != nil {
			row.Builtin = false
			return row, nil
		}
	}
	for i := range s.builtinConnectors {
		if s.builtinConnectors[i].Slug == slug {
			c := s.builtinConnectors[i]
			return &c, nil
		}
	}
	return nil, nil
}

// connectorCredentialsFromValues maps a flat field-value map (keyed by the full
// variable name a skill will read, e.g. "spotify_client_id") into the
// ConnectionCredentials shape. Well-known suffixes land on the struct fields;
// everything else is stored in the Extra bag under its full key — matching how
// workflow.ConnectionCredentialForKey resolves them at runtime.
func connectorCredentialsFromValues(values map[string]string) service.ConnectionCredentials {
	creds := service.ConnectionCredentials{}
	for k, v := range values {
		if v == "" {
			continue
		}
		_, suffix, ok := workflow.ResolveConnectionKey(k)
		switch {
		case ok && suffix == "_client_id":
			creds.ClientID = v
		case ok && suffix == "_client_secret":
			creds.ClientSecret = v
		case ok && suffix == "_refresh_token":
			creds.RefreshToken = v
		case ok && suffix == "_api_key":
			creds.APIKey = v
		default:
			if creds.Extra == nil {
				creds.Extra = map[string]string{}
			}
			creds.Extra[k] = v
		}
	}
	return creds
}
