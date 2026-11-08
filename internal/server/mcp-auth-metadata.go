package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/rakunlabs/at/internal/service"
)

// Client ID Metadata Documents (draft-ietf-oauth-client-id-metadata-document,
// the MCP 2025-11-25 default): a client_id that is an https URL names a JSON
// document describing the client. AT fetches it through the public-address
// client used for remote images (https only, no private, loopback,
// link-local or CGNAT addresses, checked at connect time so redirects and
// DNS rebinding are covered), bounds the body, and caches the result.

const (
	mcpAuthMetadataMaxBytes = 16 << 10
	mcpAuthMetadataTimeout  = 5 * time.Second
	mcpAuthMetadataMinTTL   = 5 * time.Minute
	mcpAuthMetadataMaxTTL   = 24 * time.Hour
	mcpAuthMetadataDefault  = time.Hour
	// mcpAuthMetadataFailTTL caches a failed fetch so a client_id cannot be
	// used to make AT fetch a URL on every request.
	mcpAuthMetadataFailTTL = time.Minute
)

// mcpAuthMetadataFetch fetches a document. Tests replace it.
var mcpAuthMetadataFetch = fetchMCPAuthClientMetadata

var (
	mcpAuthMetadataGroup    singleflight.Group
	mcpAuthMetadataFailures sync.Map // client_id → time.Time (retry after)
	mcpAuthMetadataLimiter  = newWebhookRateLimiter(30)
)

type mcpAuthMetadataResult struct {
	metadata service.MCPAuthClientMetadata
	ttl      time.Duration
}

func fetchMCPAuthClientMetadata(ctx context.Context, clientID string) (*mcpAuthMetadataResult, error) {
	ctx, cancel := context.WithTimeout(ctx, mcpAuthMetadataTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, clientID, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid client_id URL")
	}
	req.Header.Set("Accept", "application/json")
	resp, err := publicImageClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch client metadata: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch client metadata: status %d", resp.StatusCode)
	}
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || (mt != "application/json" && !strings.HasSuffix(mt, "+json")) {
		return nil, fmt.Errorf("client metadata must be served as application/json")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, mcpAuthMetadataMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read client metadata: %w", err)
	}
	if len(body) > mcpAuthMetadataMaxBytes {
		return nil, fmt.Errorf("client metadata is larger than %d bytes", mcpAuthMetadataMaxBytes)
	}
	var m service.MCPAuthClientMetadata
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("client metadata is not valid JSON")
	}
	return &mcpAuthMetadataResult{metadata: m, ttl: metadataCacheTTL(resp.Header.Get("Cache-Control"))}, nil
}

// metadataCacheTTL honours max-age within bounds; no-store/no-cache use the
// minimum so the document is still not fetched on every request.
func metadataCacheTTL(header string) time.Duration {
	ttl := mcpAuthMetadataDefault
	for _, part := range strings.Split(strings.ToLower(header), ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "no-store" || part == "no-cache":
			return mcpAuthMetadataMinTTL
		case strings.HasPrefix(part, "max-age="):
			if n, err := strconv.Atoi(strings.TrimPrefix(part, "max-age=")); err == nil {
				ttl = time.Duration(n) * time.Second
			}
		}
	}
	return min(max(ttl, mcpAuthMetadataMinTTL), mcpAuthMetadataMaxTTL)
}

// resolveMCPAuthClient returns the client for a client_id. A metadata-document
// client is served from its cached row while fresh and fetched otherwise; a
// failed refresh of a previously valid document keeps the cached copy for at
// most its maximum lifetime, so a transient outage of the client's host does
// not sign everyone out of it.
func (s *Server) resolveMCPAuthClient(r *http.Request, clientID string) (*service.MCPAuthClient, error) {
	store := s.mcpAuthStore()
	if store == nil {
		return nil, errors.New("authorization server unavailable")
	}
	cached, err := store.GetMCPAuthClient(r.Context(), clientID)
	if err != nil {
		return nil, err
	}
	if !service.MCPAuthMetadataClientID(clientID) {
		return cached, nil
	}
	if cached != nil && !cached.MetadataDocument {
		return cached, nil
	}
	if cached != nil {
		if exp, err := time.Parse(time.RFC3339, cached.MetadataExpiresAt); err == nil && time.Now().Before(exp) {
			return cached, nil
		}
	}
	if until, ok := mcpAuthMetadataFailures.Load(clientID); ok && time.Now().Before(until.(time.Time)) {
		return cached, nil
	}
	if cached == nil && !mcpAuthMetadataLimiter.Allow(s.clientIPs.LimitKey(r)) {
		return nil, nil
	}
	v, err, _ := mcpAuthMetadataGroup.Do(clientID, func() (any, error) {
		res, err := mcpAuthMetadataFetch(context.WithoutCancel(r.Context()), clientID)
		if err == nil {
			err = service.ValidateMCPAuthClientMetadata(clientID, res.metadata)
		}
		if err != nil {
			mcpAuthMetadataFailures.Store(clientID, time.Now().Add(mcpAuthMetadataFailTTL))
			return nil, err
		}
		mcpAuthMetadataFailures.Delete(clientID)
		return store.UpsertMetadataMCPAuthClient(r.Context(), service.MCPAuthClient{
			ClientID: clientID, Name: cleanMCPAuthClientName(res.metadata.ClientName), RedirectURIs: res.metadata.RedirectURIs,
			ClientURI: res.metadata.ClientURI, MetadataExpiresAt: time.Now().Add(res.ttl).UTC().Format(time.RFC3339),
		})
	})
	if err != nil {
		slog.Warn("MCP client metadata unavailable", "client_id", clientID, "error", err)
		// Stale-if-error: a document that expired less than a day ago keeps
		// working while its host is unreachable.
		if cached != nil {
			if exp, perr := time.Parse(time.RFC3339, cached.MetadataExpiresAt); perr == nil && time.Since(exp) < mcpAuthMetadataMaxTTL {
				return cached, nil
			}
		}
		return nil, &mcpAuthMetadataError{err: err}
	}
	return v.(*service.MCPAuthClient), nil
}

// mcpAuthMetadataError is shown to the person on the authorize page, never
// redirected: the redirect URIs come from the document that failed.
type mcpAuthMetadataError struct{ err error }

func (e *mcpAuthMetadataError) Error() string {
	return "the client's metadata document could not be used: " + e.err.Error()
}

// startMCPAuthJanitor sweeps expired codes/tokens and dynamic or
// metadata-document clients that never obtained a grant within a day.
func (s *Server) startMCPAuthJanitor(ctx context.Context) {
	store := s.mcpAuthStore()
	if store == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			if n, err := store.SweepMCPAuthClients(ctx, time.Now().Add(-service.MCPAuthUnusedClientAge)); err != nil {
				slog.Warn("MCP auth janitor failed", "error", err)
			} else if n > 0 {
				slog.Info("MCP auth janitor removed unused clients", "count", n)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
