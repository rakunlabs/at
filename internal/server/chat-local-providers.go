package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// localChatProvidersKey names the per-account registry of OpenAI-compatible
// endpoints the account's browser calls directly from Chats. It lives in the
// existing user_preferences table with Secret: true, so the API key and
// header values are encrypted and no migration is needed.
const localChatProvidersKey = "local_chat_providers"

const localChatProviderRegistryMaxBytes = 192 << 10

type localChatProviderRegistry struct {
	Providers []service.LocalChatProvider `json:"providers"`
}

func (s *Server) loadLocalChatProviders(ctx context.Context, owner string) ([]service.LocalChatProvider, error) {
	pref, err := s.userPrefStore.GetUserPreference(ctx, owner, localChatProvidersKey)
	if err != nil {
		return nil, err
	}
	if pref == nil || len(pref.Value) == 0 {
		return nil, nil
	}
	var stored localChatProviderRegistry
	if err := json.Unmarshal(pref.Value, &stored); err != nil {
		// A registry written by a newer client must not break the page.
		return nil, nil
	}

	return stored.Providers, nil
}

// redactLocalChatProviders replaces the API key and header values with the
// sentinel. Header names stay visible because the user configured them.
func redactLocalChatProviders(providers []service.LocalChatProvider) []service.LocalChatProvider {
	out := make([]service.LocalChatProvider, 0, len(providers))
	for _, p := range providers {
		if p.APIKey != "" {
			p.APIKey = redactedSecret
		}
		if len(p.Headers) > 0 {
			headers := make(map[string]string, len(p.Headers))
			for k := range p.Headers {
				headers[k] = redactedSecret
			}
			p.Headers = headers
		}
		out = append(out, p)
	}

	return out
}

// LocalChatProvidersAPI handles GET and PUT /api/v1/chats/local-providers.
//
// The endpoint stores and validates. Nothing in the server reads this
// preference key to make a request: the browser calls these endpoints, which
// is what lets "localhost" mean the user's machine.
func (s *Server) LocalChatProvidersAPI(w http.ResponseWriter, r *http.Request) {
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
		stored, err := s.loadLocalChatProviders(r.Context(), owner)
		if err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}
		httpResponseJSON(w, localChatProviderRegistry{Providers: redactLocalChatProviders(stored)}, http.StatusOK)
	case http.MethodPut:
		var req localChatProviderRegistry
		if !decodePlaygroundBody(w, r, &req) {
			return
		}

		stored, err := s.loadLocalChatProviders(r.Context(), owner)
		if err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}

		merged, err := mergeLocalChatProviders(stored, req.Providers)
		if err != nil {
			nativeError(w, http.StatusBadRequest, err.Error())
			return
		}
		normalized, err := service.NormalizeLocalChatProviders(merged)
		if err != nil {
			nativeError(w, http.StatusBadRequest, err.Error())
			return
		}

		encoded, err := json.Marshal(localChatProviderRegistry{Providers: normalized})
		if err != nil {
			nativeError(w, http.StatusBadRequest, "invalid local provider registry")
			return
		}
		if len(encoded) > localChatProviderRegistryMaxBytes {
			nativeError(w, http.StatusRequestEntityTooLarge, "local provider registry is too large")
			return
		}
		if err := s.userPrefStore.SetUserPreference(r.Context(), service.UserPreference{
			UserID: owner,
			Key:    localChatProvidersKey,
			Value:  json.RawMessage(encoded),
			Secret: true,
		}); err != nil {
			nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
			return
		}

		httpResponseJSON(w, localChatProviderRegistry{Providers: redactLocalChatProviders(normalized)}, http.StatusOK)
	default:
		nativeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// mergeLocalChatProviders mints IDs, preserves creation time and restores
// secrets the client sent back as the redaction sentinel. A sentinel with no
// stored value behind it is an error rather than a stored "***", which would
// otherwise fail later at the provider with an unrelated authentication error.
func mergeLocalChatProviders(stored, submitted []service.LocalChatProvider) ([]service.LocalChatProvider, error) {
	byID := make(map[string]service.LocalChatProvider, len(stored))
	for _, p := range stored {
		byID[p.ID] = p
	}

	now := time.Now().UTC().Format(time.RFC3339)
	out := make([]service.LocalChatProvider, 0, len(submitted))
	for _, p := range submitted {
		prev, existed := byID[p.ID]
		if p.ID == "" || !existed {
			p.ID = ulid.Make().String()
			p.CreatedAt = now
		} else {
			p.CreatedAt = prev.CreatedAt
		}
		p.UpdatedAt = now

		if p.APIKey == redactedSecret {
			if !existed || prev.APIKey == "" {
				return nil, fmt.Errorf("provider %q: API key has no stored value to preserve", strings.TrimSpace(p.Name))
			}
			p.APIKey = prev.APIKey
		}
		for key, value := range p.Headers {
			if value != redactedSecret {
				continue
			}
			kept, ok := prev.Headers[key]
			if !existed || !ok {
				return nil, fmt.Errorf("provider %q: header %q has no stored value to preserve", strings.TrimSpace(p.Name), key)
			}
			p.Headers[key] = kept
		}

		out = append(out, p)
	}

	return out, nil
}

// LocalChatProviderRevealAPI handles POST
// /api/v1/chats/local-providers/{id}/reveal.
//
// Credentials are revealed per record, when the browser is about to call the
// provider, so the page does not hold every key for the whole session. POST
// because the response is a secret and must not be cached.
func (s *Server) LocalChatProviderRevealAPI(w http.ResponseWriter, r *http.Request) {
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
		nativeError(w, http.StatusBadRequest, "provider id is required")
		return
	}

	stored, err := s.loadLocalChatProviders(r.Context(), owner)
	if err != nil {
		nativeError(w, http.StatusServiceUnavailable, "preference storage unavailable")
		return
	}
	for _, p := range stored {
		if p.ID != id {
			continue
		}
		headers := p.Headers
		if headers == nil {
			headers = map[string]string{}
		}
		httpResponseJSON(w, map[string]any{"api_key": p.APIKey, "headers": headers}, http.StatusOK)

		return
	}

	nativeError(w, http.StatusNotFound, "local provider not found")
}
