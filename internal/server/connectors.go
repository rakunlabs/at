package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Connector Registry API ───
//
// Connectors are the data-driven definitions of external-service connection
// TYPES (provider catalog). The catalog is the merge of built-in JSON
// definitions and user-defined / override rows in the connectors table. These
// endpoints expose CRUD so the UI can add new providers without a code change.

var connectorSlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// connectorRequest is the create/update body for a connector.
type connectorRequest struct {
	Slug        string                   `json:"slug"`
	Name        string                   `json:"name"`
	Description string                   `json:"description"`
	Icon        string                   `json:"icon"`
	AuthKind    string                   `json:"auth_kind"`
	OAuth       *service.ConnectorOAuth  `json:"oauth"`
	Fields      []service.ConnectorField `json:"fields"`
}

func validConnectorAuthKind(kind string) bool {
	switch kind {
	case service.ConnectorAuthOAuth2, service.ConnectorAuthToken, service.ConnectorAuthCustom:
		return true
	default:
		return false
	}
}

// ListConnectorsAPI returns the merged connector catalog (built-ins + DB).
// GET /api/v1/connectors
func (s *Server) ListConnectorsAPI(w http.ResponseWriter, r *http.Request) {
	connectors, err := s.listConnectors(r.Context())
	if err != nil {
		slog.Error("list connectors failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to list connectors: %v", err), http.StatusInternalServerError)
		return
	}
	httpResponseJSON(w, connectors, http.StatusOK)
}

// GetConnectorAPI returns a single connector (DB override wins over built-in).
// GET /api/v1/connectors/{slug}
func (s *Server) GetConnectorAPI(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if slug == "" {
		httpResponse(w, "connector slug is required", http.StatusBadRequest)
		return
	}
	c, err := s.resolveConnector(r.Context(), slug)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to get connector: %v", err), http.StatusInternalServerError)
		return
	}
	if c == nil {
		httpResponse(w, fmt.Sprintf("connector %q not found", slug), http.StatusNotFound)
		return
	}
	httpResponseJSON(w, c, http.StatusOK)
}

// CreateConnectorAPI creates a user-defined connector (or an override of a
// built-in, when the slug matches one).
// POST /api/v1/connectors
func (s *Server) CreateConnectorAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectorStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req connectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	c, problem := normalizeConnectorRequest(req)
	if problem != "" {
		httpResponse(w, problem, http.StatusBadRequest)
		return
	}

	// Reject duplicate user rows (overrides of built-ins are still inserts, but
	// a second DB row for the same slug is a conflict).
	if existing, _ := s.connectorStore.GetConnector(r.Context(), c.Slug); existing != nil {
		httpResponse(w, fmt.Sprintf("connector %q already exists", c.Slug), http.StatusConflict)
		return
	}

	userEmail := s.getUserEmail(r)
	c.CreatedBy = userEmail
	c.UpdatedBy = userEmail

	rec, err := s.connectorStore.CreateConnector(r.Context(), c)
	if err != nil {
		if isUniqueViolation(err) {
			httpResponse(w, fmt.Sprintf("connector %q already exists", c.Slug), http.StatusConflict)
			return
		}
		slog.Error("create connector failed", "slug", c.Slug, "error", err)
		httpResponse(w, fmt.Sprintf("failed to create connector: %v", err), http.StatusInternalServerError)
		return
	}

	httpResponseJSON(w, rec, http.StatusCreated)
}

// UpdateConnectorAPI updates a connector. Editing a built-in for the first time
// persists a DB override row.
// PUT /api/v1/connectors/{slug}
func (s *Server) UpdateConnectorAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectorStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	slug := r.PathValue("slug")
	if slug == "" {
		httpResponse(w, "connector slug is required", http.StatusBadRequest)
		return
	}

	var req connectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	// Slug is taken from the path; ignore any body slug mismatch.
	req.Slug = slug

	c, problem := normalizeConnectorRequest(req)
	if problem != "" {
		httpResponse(w, problem, http.StatusBadRequest)
		return
	}

	userEmail := s.getUserEmail(r)
	c.UpdatedBy = userEmail

	existing, err := s.connectorStore.GetConnector(r.Context(), slug)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to load connector: %v", err), http.StatusInternalServerError)
		return
	}

	// No DB row yet: this is either a brand-new connector or the first edit of a
	// built-in. Either way, insert a row (an override for built-ins).
	if existing == nil {
		c.CreatedBy = userEmail
		rec, err := s.connectorStore.CreateConnector(r.Context(), c)
		if err != nil {
			slog.Error("create connector override failed", "slug", slug, "error", err)
			httpResponse(w, fmt.Sprintf("failed to save connector: %v", err), http.StatusInternalServerError)
			return
		}
		httpResponseJSON(w, rec, http.StatusOK)
		return
	}

	rec, err := s.connectorStore.UpdateConnector(r.Context(), slug, c)
	if err != nil {
		slog.Error("update connector failed", "slug", slug, "error", err)
		httpResponse(w, fmt.Sprintf("failed to update connector: %v", err), http.StatusInternalServerError)
		return
	}
	if rec == nil {
		httpResponse(w, fmt.Sprintf("connector %q not found", slug), http.StatusNotFound)
		return
	}

	httpResponseJSON(w, rec, http.StatusOK)
}

// DeleteConnectorAPI removes a user-defined connector or reverts a built-in
// override back to its built-in definition. Pure built-ins (no DB row) cannot
// be deleted.
// DELETE /api/v1/connectors/{slug}
func (s *Server) DeleteConnectorAPI(w http.ResponseWriter, r *http.Request) {
	if s.connectorStore == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	slug := r.PathValue("slug")
	if slug == "" {
		httpResponse(w, "connector slug is required", http.StatusBadRequest)
		return
	}

	existing, err := s.connectorStore.GetConnector(r.Context(), slug)
	if err != nil {
		httpResponse(w, fmt.Sprintf("failed to load connector: %v", err), http.StatusInternalServerError)
		return
	}
	if existing == nil {
		// No DB row — either unknown or a pure built-in.
		if s.isBuiltinConnector(slug) {
			httpResponse(w, "built-in connectors cannot be deleted; edit it to override instead", http.StatusBadRequest)
			return
		}
		httpResponse(w, fmt.Sprintf("connector %q not found", slug), http.StatusNotFound)
		return
	}

	if err := s.connectorStore.DeleteConnector(r.Context(), slug); err != nil {
		slog.Error("delete connector failed", "slug", slug, "error", err)
		httpResponse(w, fmt.Sprintf("failed to delete connector: %v", err), http.StatusInternalServerError)
		return
	}

	reverted := s.isBuiltinConnector(slug)
	httpResponseJSON(w, map[string]any{
		"status":   "deleted",
		"slug":     slug,
		"reverted": reverted, // true when a built-in override was removed
	}, http.StatusOK)
}

// ─── Helpers ───

func (s *Server) isBuiltinConnector(slug string) bool {
	for i := range s.builtinConnectors {
		if s.builtinConnectors[i].Slug == slug {
			return true
		}
	}
	return false
}

// normalizeConnectorRequest validates and converts a request into a Connector,
// returning a human-readable problem string when invalid.
func normalizeConnectorRequest(req connectorRequest) (service.Connector, string) {
	slug := strings.TrimSpace(strings.ToLower(req.Slug))
	if slug == "" {
		return service.Connector{}, "slug is required"
	}
	if !connectorSlugRe.MatchString(slug) {
		return service.Connector{}, "slug must be lowercase alphanumeric with - or _ (max 64 chars)"
	}

	kind := strings.TrimSpace(req.AuthKind)
	if kind == "" {
		kind = service.ConnectorAuthToken
	}
	if !validConnectorAuthKind(kind) {
		return service.Connector{}, "auth_kind must be one of: oauth2, token, custom"
	}

	c := service.Connector{
		Slug:        slug,
		Name:        strings.TrimSpace(req.Name),
		Description: strings.TrimSpace(req.Description),
		Icon:        strings.TrimSpace(req.Icon),
		AuthKind:    kind,
		OAuth:       req.OAuth,
		Fields:      req.Fields,
	}
	if c.Name == "" {
		c.Name = slug
	}

	if kind == service.ConnectorAuthOAuth2 {
		if c.OAuth == nil || strings.TrimSpace(c.OAuth.AuthURL) == "" || strings.TrimSpace(c.OAuth.TokenURL) == "" {
			return service.Connector{}, "oauth2 connectors require oauth.auth_url and oauth.token_url"
		}
	}

	// Validate field types.
	for i := range c.Fields {
		if c.Fields[i].Key == "" {
			return service.Connector{}, "every field requires a key"
		}
		if c.Fields[i].Type == "" {
			c.Fields[i].Type = service.ConnectorFieldText
		}
		if c.Fields[i].Type != service.ConnectorFieldText && c.Fields[i].Type != service.ConnectorFieldSecret {
			return service.Connector{}, fmt.Sprintf("field %q type must be text or secret", c.Fields[i].Key)
		}
	}

	return c, ""
}
