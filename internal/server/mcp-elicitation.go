package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// MCP elicitation: an upstream MCP server asks the person for input in the
// middle of a tool call. The question waits in elicitationBroker, owned by the
// account and workspace that runs the call; the page shows it (Sessions and
// Developer Spaces get a stream event, Chats polls while an MCP tool runs) and
// answers through POST /api/v1/mcp/elicitations/{id}. Questions are in
// memory on the replica running the call, like confirmations and steering.

const elicitationMaxPending = 16

type pendingElicitation struct {
	ID        string         `json:"id"`
	Scope     string         `json:"scope"`
	Server    string         `json:"server"`
	Mode      string         `json:"mode"`
	Message   string         `json:"message"`
	Schema    map[string]any `json:"requested_schema,omitempty"`
	CreatedAt string         `json:"created_at"`
	// URL mode: the page to open, its host (shown prominently against
	// look-alike subdomains), and whether the tool call waits for it to be
	// finished (-32042) rather than only for consent.
	URL           string `json:"url,omitempty"`
	Host          string `json:"host,omitempty"`
	Punycode      bool   `json:"punycode,omitempty"`
	ElicitationID string `json:"elicitation_id,omitempty"`
	Required      bool   `json:"required,omitempty"`

	workspaceID string
	userID      string
	answer      chan service.ElicitResult
}

type elicitationBroker struct {
	mu      sync.Mutex
	pending map[string]*pendingElicitation
	// changed is closed and replaced whenever a question is added or
	// removed, so long-polls wake up.
	changed chan struct{}
}

func newElicitationBroker() *elicitationBroker {
	return &elicitationBroker{pending: map[string]*pendingElicitation{}, changed: make(chan struct{})}
}

func (b *elicitationBroker) notifyLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

// elicitor returns the Elicitor for a run of userID in workspaceID. scope
// names the surface ("chats", "session:<id>", "developer:<id>").
func (b *elicitationBroker) elicitor(workspaceID, userID, scope string) service.Elicitor {
	return func(ctx context.Context, req service.ElicitRequest) (service.ElicitResult, error) {
		if b == nil || userID == "" {
			return service.ElicitResult{Action: service.ElicitCancel}, nil
		}
		p := &pendingElicitation{
			ID: ulid.Make().String(), Scope: scope, Server: req.Server, Mode: req.Mode, Message: req.Message,
			Schema: req.RequestedSchema, CreatedAt: time.Now().UTC().Format(time.RFC3339),
			URL: req.URL, ElicitationID: req.ElicitationID, Required: req.Required,
			workspaceID: workspaceID, userID: userID, answer: make(chan service.ElicitResult, 1),
		}
		if p.Mode == "" {
			p.Mode = service.ElicitModeForm
		}
		if u, err := url.Parse(p.URL); err == nil && p.URL != "" {
			p.Host = u.Hostname()
			p.Punycode = strings.Contains(strings.ToLower(p.Host), "xn--")
		}
		b.mu.Lock()
		count := 0
		for _, other := range b.pending {
			if other.userID == userID {
				count++
			}
		}
		if count >= elicitationMaxPending {
			b.mu.Unlock()
			return service.ElicitResult{Action: service.ElicitCancel}, nil
		}
		b.pending[p.ID] = p
		b.notifyLocked()
		b.mu.Unlock()
		defer func() {
			b.mu.Lock()
			if _, ok := b.pending[p.ID]; ok {
				delete(b.pending, p.ID)
				b.notifyLocked()
			}
			b.mu.Unlock()
		}()
		select {
		case res := <-p.answer:
			return res, nil
		case <-ctx.Done():
			return service.ElicitResult{}, ctx.Err()
		}
	}
}

// complete handles notifications/elicitation/complete: a URL elicitation the
// tool call is waiting on was finished, so it continues as if the person had
// confirmed. Unknown IDs are ignored, as the protocol requires.
func (b *elicitationBroker) complete(workspaceID, userID, elicitationID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, p := range b.pending {
		if p.userID == userID && p.workspaceID == workspaceID && p.Mode == service.ElicitModeURL && p.ElicitationID == elicitationID {
			delete(b.pending, id)
			b.notifyLocked()
			p.answer <- service.ElicitResult{Action: service.ElicitAccept}
		}
	}
}

// list returns the caller's questions, oldest first, and a channel closed on
// the next change.
func (b *elicitationBroker) list(workspaceID, userID string) ([]*pendingElicitation, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []*pendingElicitation{}
	for _, p := range b.pending {
		if p.userID == userID && p.workspaceID == workspaceID {
			out = append(out, p)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].ID < out[j-1].ID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out, b.changed
}

// answer delivers result to a question the caller owns. A foreign or unknown
// question reads as not found.
func (b *elicitationBroker) answer(workspaceID, userID, id string, result service.ElicitResult) (bool, error) {
	b.mu.Lock()
	p := b.pending[id]
	if p == nil || p.userID != userID || p.workspaceID != workspaceID {
		b.mu.Unlock()
		return false, nil
	}
	b.mu.Unlock()
	normalized, err := service.NormalizeElicitResult(p.Schema, result)
	if err != nil {
		return true, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending[id] != p {
		return false, nil
	}
	delete(b.pending, id)
	b.notifyLocked()
	p.answer <- normalized
	return true, nil
}

// contextWithElicitation installs the broker's elicitor. Only surfaces with a
// person watching install it; the page polls ListMCPElicitationsAPI while a
// turn runs.
func (s *Server) contextWithElicitation(ctx context.Context, scope string) context.Context {
	if s.elicitations == nil {
		return ctx
	}
	// The bound run's account; before binding, the signed-in principal.
	workspaceID, userID := "", ""
	if provenance, _, ok := service.ExecutionFromContext(ctx); ok {
		workspaceID, userID = provenance.WorkspaceID, provenance.UserID
	} else if principal, ok := service.AccessPrincipalFromContext(ctx); ok {
		workspaceID, userID = principal.WorkspaceID, principal.UserID
	}
	if userID == "" {
		return ctx
	}
	ctx = service.ContextWithElicitationComplete(ctx, func(elicitationID string) {
		s.elicitations.complete(workspaceID, userID, elicitationID)
	})
	return service.ContextWithElicitor(ctx, s.elicitations.elicitor(workspaceID, userID, scope))
}

func elicitationCaller(r *http.Request) (string, string, bool) {
	principal, ok := service.AccessPrincipalFromContext(r.Context())
	if !ok || principal.UserID == "" {
		return "", "", false
	}
	return principal.WorkspaceID, principal.UserID, true
}

// ListMCPElicitationsAPI handles GET /api/v1/mcp/elicitations[?wait=seconds]:
// the caller's open questions. With wait (≤25s) it returns as soon as the
// list differs from empty or changes.
func (s *Server) ListMCPElicitationsAPI(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := elicitationCaller(r)
	if !ok || s.elicitations == nil {
		httpResponseJSON(w, map[string]any{"elicitations": []any{}}, http.StatusOK)
		return
	}
	items, changed := s.elicitations.list(workspaceID, userID)
	if wait, _ := strconv.Atoi(r.URL.Query().Get("wait")); wait > 0 && len(items) == 0 {
		timer := time.NewTimer(time.Duration(min(wait, 25)) * time.Second)
		defer timer.Stop()
		select {
		case <-changed:
			items, _ = s.elicitations.list(workspaceID, userID)
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
	}
	httpResponseJSON(w, map[string]any{"elicitations": items}, http.StatusOK)
}

// AnswerMCPElicitationAPI handles POST /api/v1/mcp/elicitations/{id} with
// {action: accept|decline|cancel, content?}.
func (s *Server) AnswerMCPElicitationAPI(w http.ResponseWriter, r *http.Request) {
	workspaceID, userID, ok := elicitationCaller(r)
	if !ok || s.elicitations == nil {
		httpResponse(w, "question not found", http.StatusNotFound)
		return
	}
	var req service.ElicitResult
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}
	found, err := s.elicitations.answer(workspaceID, userID, r.PathValue("id"), req)
	if !found {
		httpResponse(w, "question not found or already answered (questions live on the server that asked them)", http.StatusNotFound)
		return
	}
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	httpResponseJSON(w, map[string]any{"status": "ok"}, http.StatusOK)
}
