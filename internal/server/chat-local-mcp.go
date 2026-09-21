package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
)

// localMCPServersKey names the per-account registry of MCP servers running on
// the account holder's own machines, inside the existing user_preferences
// table — so a personal registry needs no migration and no new table.
//
// The row is written with Secret: true, which encrypts the whole value. That
// matters for the header map; the URL rides along and is redundant to
// encrypt, which is cheaper than splitting one record across two rows.
const localMCPServersKey = "local_mcp_servers"

// localMCPRegistryMaxBytes bounds the stored blob. Per-entry bounds are
// enforced by service.NormalizeLocalMCPServers; this is the backstop.
const localMCPRegistryMaxBytes = 128 << 10

// localMCPRegistry is the stored shape. A wrapper object rather than a bare
// array, so a later field does not need a value migration.
type localMCPRegistry struct {
	Servers []service.LocalMCPServer `json:"servers"`
}

// loadLocalMCPServers reads the owner's registry. A missing row is an empty
// registry, not an error: the client always asks.
func (s *Server) loadLocalMCPServers(ctx context.Context, owner string) ([]service.LocalMCPServer, error) {
	pref, err := s.userPrefStore.GetUserPreference(ctx, owner, localMCPServersKey)
	if err != nil {
		return nil, err
	}
	if pref == nil || len(pref.Value) == 0 {
		return nil, nil
	}
	var stored localMCPRegistry
	if err := json.Unmarshal(pref.Value, &stored); err != nil {
		// A registry written by a newer client must not break the page.
		return nil, nil
	}

	return stored.Servers, nil
}

// redactLocalMCPServers returns a copy safe to send on an ordinary read.
// Header values are replaced by the sentinel the rest of the product uses;
// the key names stay visible because the user configured them.
func redactLocalMCPServers(servers []service.LocalMCPServer) []service.LocalMCPServer {
	out := make([]service.LocalMCPServer, 0, len(servers))
	for _, srv := range servers {
		if len(srv.Headers) > 0 {
			headers := make(map[string]string, len(srv.Headers))
			for k := range srv.Headers {
				headers[k] = redactedSecret
			}
			srv.Headers = headers
		}
		out = append(out, srv)
	}

	return out
}

// LocalMCPServersAPI handles GET and PUT /api/v1/chats/local-mcp-servers.
//
// The owner is resolved by playgroundAccess (the authenticated subject) and is
// never read from the request, matching the rest of the Chats surface.
//
// This endpoint stores and validates. It does not dial, and nothing else in
// the server reads this preference key: a registry entry addresses the
// caller's own machine, so a server-side connection would be a request from AT
// into AT's own network, chosen by any account.
func (s *Server) LocalMCPServersAPI(w http.ResponseWriter, r *http.Request) {
	_, owner := s.playgroundAccess(w, r)
	if owner == "" {
		return
	}
	if s.userPrefStore == nil {
		nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
		return
	}

	switch r.Method {
	case http.MethodGet:
		stored, err := s.loadLocalMCPServers(r.Context(), owner)
		if err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}
		httpResponseJSON(w, localMCPRegistry{Servers: redactLocalMCPServers(stored)}, http.StatusOK)
	case http.MethodPut:
		var req localMCPRegistry
		if !decodePlaygroundBody(w, r, &req) {
			return
		}

		stored, err := s.loadLocalMCPServers(r.Context(), owner)
		if err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}

		merged, err := mergeLocalMCPServers(stored, req.Servers)
		if err != nil {
			nativeError(w, http.StatusBadRequest, err.Error())
			return
		}
		normalized, err := service.NormalizeLocalMCPServers(merged)
		if err != nil {
			nativeError(w, http.StatusBadRequest, err.Error())
			return
		}

		encoded, err := json.Marshal(localMCPRegistry{Servers: normalized})
		if err != nil {
			nativeError(w, http.StatusBadRequest, "invalid local MCP registry")
			return
		}
		if len(encoded) > localMCPRegistryMaxBytes {
			nativeError(w, http.StatusRequestEntityTooLarge, "local MCP registry is too large")
			return
		}
		if err := s.userPrefStore.SetUserPreference(r.Context(), service.UserPreference{
			UserID: owner,
			Key:    localMCPServersKey,
			Value:  json.RawMessage(encoded),
			Secret: true,
		}); err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}

		httpResponseJSON(w, localMCPRegistry{Servers: redactLocalMCPServers(normalized)}, http.StatusOK)
	default:
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// mergeLocalMCPServers resolves a submitted registry against what is stored:
// it mints IDs for new entries, preserves creation time, stamps the update
// time, and restores header values the client sent back as the redaction
// sentinel.
//
// Sentinel preservation exists because every writer submits the whole record
// and would otherwise wipe a secret it never saw. A sentinel with nothing
// behind it is an error rather than an empty value — silently storing "***"
// as a credential would fail later, at the MCP server, with an error that
// says nothing about this form.
func mergeLocalMCPServers(stored, submitted []service.LocalMCPServer) ([]service.LocalMCPServer, error) {
	byID := make(map[string]service.LocalMCPServer, len(stored))
	for _, srv := range stored {
		byID[srv.ID] = srv
	}

	now := time.Now().UTC().Format(time.RFC3339)
	out := make([]service.LocalMCPServer, 0, len(submitted))
	for _, srv := range submitted {
		prev, existed := byID[srv.ID]
		if srv.ID == "" || !existed {
			srv.ID = ulid.Make().String()
			srv.CreatedAt = now
		} else {
			srv.CreatedAt = prev.CreatedAt
		}
		srv.UpdatedAt = now

		for key, value := range srv.Headers {
			if value != redactedSecret {
				continue
			}
			kept, ok := prev.Headers[key]
			if !existed || !ok {
				return nil, fmt.Errorf("header %q has no stored value to preserve", key)
			}
			srv.Headers[key] = kept
		}

		out = append(out, srv)
	}

	return out, nil
}

// LocalMCPServerRevealAPI handles POST
// /api/v1/chats/local-mcp-servers/{id}/reveal.
//
// Headers are revealed per record, at the point the browser is about to dial
// it, rather than being handed out with the list — so a page does not hold
// every credential for the whole session. POST rather than GET because the
// response is a secret and must not be cached or replayed from a URL.
func (s *Server) LocalMCPServerRevealAPI(w http.ResponseWriter, r *http.Request) {
	_, owner := s.playgroundAccess(w, r)
	if owner == "" {
		return
	}
	if s.userPrefStore == nil {
		nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
		return
	}

	id := r.PathValue("id")
	if id == "" {
		nativeError(w, http.StatusBadRequest, "server id is required")
		return
	}

	stored, err := s.loadLocalMCPServers(r.Context(), owner)
	if err != nil {
		nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
		return
	}
	for _, srv := range stored {
		if srv.ID != id {
			continue
		}
		headers := srv.Headers
		if headers == nil {
			headers = map[string]string{}
		}
		httpResponseJSON(w, map[string]any{"headers": headers}, http.StatusOK)

		return
	}

	// A record of another account is indistinguishable from one that does not
	// exist, matching the rest of the owner-scoped Chats surface.
	nativeError(w, http.StatusNotFound, "local MCP server not found")
}
