package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// Elicitation relay for AT's gateway MCP endpoint. When an upstream MCP
// server asks a question during a tools/call that an external MCP client
// (Claude Code, Cursor, …) made through AT, the question is forwarded to that
// client on the call's SSE response as an elicitation/create request, and
// the client's answer — a separate POST carrying only the JSON-RPC response —
// is passed back upstream. Completion notifications of URL elicitations are
// forwarded the same way, and a -32042 URLElicitationRequiredError reaches
// the client intact so it can open the URL and retry itself.
//
// A relay needs to know the client: initialize now mints an Mcp-Session-Id
// and records the client's elicitation modes for it. Sessions and pending
// questions are in memory on the replica that served initialize, so
// multiple replicas need sticky routing for this to work; without a known
// session, questions are answered "cancel" as before.

const (
	gatewayMCPSessionTTL = 24 * time.Hour
	gatewayMCPSessionMax = 10000
	gatewayElicitPrefix  = "at-elicit-"
)

type gatewayMCPSession struct {
	serverID   string
	credential string
	form, url  bool
	used       time.Time
}

type gatewayElicitWait struct {
	sessionID string
	answer    chan service.ElicitResult
}

type gatewayMCPSessions struct {
	mu       sync.Mutex
	sessions map[string]*gatewayMCPSession
	waits    map[string]*gatewayElicitWait
}

func newGatewayMCPSessions() *gatewayMCPSessions {
	return &gatewayMCPSessions{sessions: map[string]*gatewayMCPSession{}, waits: map[string]*gatewayElicitWait{}}
}

// gatewayMCPCredential identifies who opened a session, so another caller
// that learns the session ID cannot use it.
func gatewayMCPCredential(r *http.Request) string {
	if token := gatewayTokenFromContext(r.Context()); token != nil {
		return "token:" + token.ID
	}
	if p, _, ok := service.ExecutionFromContext(r.Context()); ok && p.GrantID != "" {
		return "grant:" + p.GrantID
	}
	return "anonymous"
}

// open records a client session and returns its ID.
func (g *gatewayMCPSessions) open(serverID, credential string, params any) string {
	form, url := false, false
	if m, ok := params.(map[string]any); ok {
		caps, _ := m["capabilities"].(map[string]any)
		if e, ok := caps["elicitation"].(map[string]any); ok {
			_, hasForm := e["form"]
			_, url = e["url"]
			// An empty object means form only (protocol 2025-11-25).
			form = hasForm || len(e) == 0
		}
	}
	id := ulid.Make().String()
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.sessions) >= gatewayMCPSessionMax {
		oldestID, oldest := "", now
		for key, s := range g.sessions {
			if now.Sub(s.used) > gatewayMCPSessionTTL {
				delete(g.sessions, key)
				continue
			}
			if s.used.Before(oldest) {
				oldestID, oldest = key, s.used
			}
		}
		if len(g.sessions) >= gatewayMCPSessionMax {
			delete(g.sessions, oldestID)
		}
	}
	g.sessions[id] = &gatewayMCPSession{serverID: serverID, credential: credential, form: form, url: url, used: now}
	return id
}

func (g *gatewayMCPSessions) lookup(id, serverID, credential string) *gatewayMCPSession {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.sessions[id]
	if s == nil || s.serverID != serverID || s.credential != credential || time.Since(s.used) > gatewayMCPSessionTTL {
		return nil
	}
	s.used = time.Now()
	copied := *s
	return &copied
}

// elicitor forwards questions of one tools/call to the client on events.
func (g *gatewayMCPSessions) elicitor(sessionID string, session *gatewayMCPSession, events chan<- any) service.Elicitor {
	return func(ctx context.Context, req service.ElicitRequest) (service.ElicitResult, error) {
		if (req.Mode == service.ElicitModeURL && !session.url) || (req.Mode != service.ElicitModeURL && !session.form) {
			return service.ElicitResult{Action: service.ElicitCancel}, nil
		}
		id := gatewayElicitPrefix + ulid.Make().String()
		params := map[string]any{"mode": req.Mode, "message": req.Message}
		if req.Mode == service.ElicitModeURL {
			params["url"], params["elicitationId"] = req.URL, req.ElicitationID
		} else {
			params["requestedSchema"] = req.RequestedSchema
		}
		wait := &gatewayElicitWait{sessionID: sessionID, answer: make(chan service.ElicitResult, 1)}
		g.mu.Lock()
		g.waits[id] = wait
		g.mu.Unlock()
		defer func() {
			g.mu.Lock()
			delete(g.waits, id)
			g.mu.Unlock()
		}()
		select {
		case events <- map[string]any{"jsonrpc": "2.0", "id": id, "method": "elicitation/create", "params": params}:
		case <-ctx.Done():
			return service.ElicitResult{}, ctx.Err()
		}
		select {
		case res := <-wait.answer:
			return res, nil
		case <-ctx.Done():
			return service.ElicitResult{}, ctx.Err()
		}
	}
}

// deliver passes a client's answer to the waiting question. It reports
// whether the ID belonged to a question of this session.
func (g *gatewayMCPSessions) deliver(id, sessionID string, result service.ElicitResult) bool {
	g.mu.Lock()
	wait := g.waits[id]
	if wait == nil || wait.sessionID != sessionID {
		g.mu.Unlock()
		return false
	}
	delete(g.waits, id)
	g.mu.Unlock()
	wait.answer <- result
	return true
}

// gatewayMCPClientResponse handles a POST that carries a JSON-RPC response
// instead of a request: the client's answer to a forwarded question.
func (s *Server) gatewayMCPClientResponse(w http.ResponseWriter, r *http.Request, srv *service.MCPServer, raw json.RawMessage) bool {
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &msg) != nil || msg.Method != "" || len(msg.ID) == 0 {
		return false
	}
	var id string
	if json.Unmarshal(msg.ID, &id) != nil || !strings.HasPrefix(id, gatewayElicitPrefix) {
		return false
	}
	sessionID := r.Header.Get("Mcp-Session-Id")
	if s.gatewayMCP.lookup(sessionID, srv.ID, gatewayMCPCredential(r)) == nil {
		httpResponse(w, "unknown MCP session", http.StatusNotFound)
		return true
	}
	result := service.ElicitResult{Action: service.ElicitCancel}
	if len(msg.Result) > 0 && len(msg.Error) == 0 {
		_ = json.Unmarshal(msg.Result, &result)
	}
	if !s.gatewayMCP.deliver(id, sessionID, result) {
		httpResponse(w, "no pending elicitation with this id", http.StatusNotFound)
		return true
	}
	w.WriteHeader(http.StatusAccepted)
	return true
}

// gatewayMCPElicitationContext installs the relay for one tools/call when
// the client's session supports elicitation and accepts SSE. events is nil
// otherwise.
func (s *Server) gatewayMCPElicitationContext(ctx context.Context, r *http.Request, srv *service.MCPServer) (context.Context, chan any) {
	if s.gatewayMCP == nil || !acceptsEventStream(r) {
		return ctx, nil
	}
	sessionID := r.Header.Get("Mcp-Session-Id")
	session := s.gatewayMCP.lookup(sessionID, srv.ID, gatewayMCPCredential(r))
	if session == nil || (!session.form && !session.url) {
		return ctx, nil
	}
	events := make(chan any, 4)
	ctx = service.ContextWithElicitor(ctx, s.gatewayMCP.elicitor(sessionID, session, events))
	ctx = service.ContextWithElicitationComplete(ctx, func(elicitationID string) {
		select {
		case events <- map[string]any{"jsonrpc": "2.0", "method": "notifications/elicitation/complete", "params": map[string]any{"elicitationId": elicitationID}}:
		default:
		}
	})
	if session.url {
		// The client opens -32042 URLs and retries the call itself.
		ctx = service.ContextWithURLElicitationPassthrough(ctx)
	}
	return ctx, events
}
