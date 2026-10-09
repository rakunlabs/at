package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/mcpauth"
)

// MCP OAuth authorization (the MCP authorization spec, client side).
//
//	POST /api/v1/mcp/oauth/start     → {authorize_url}
//	GET  /api/v1/mcp/oauth/callback  ← top-level navigation from the authorization server
//
// The pending ceremony (PKCE verifier, client credentials, pinned endpoints)
// lives encrypted in mcp_oauth_pending, bound to the starting account,
// workspace and session, so the callback may reach any replica.

const mcpOAuthCallbackPath = "/api/v1/mcp/oauth/callback"

type mcpOAuthStartRequest struct {
	// Exactly one upstream reference: an MCP set or MCP server plus the index
	// of the upstream in its config, or an existing connection to reconnect.
	SetID         string `json:"set_id,omitempty"`
	ServerID      string `json:"server_id,omitempty"`
	UpstreamIndex int    `json:"upstream_index"`
	ConnectionID  string `json:"connection_id,omitempty"`

	// Target is "personal" (default) or "shared" (a workspace connection).
	Target         string `json:"target,omitempty"`
	ConnectionName string `json:"connection_name,omitempty"`
	// ClientSecret is for a pre-registered confidential client. It is kept
	// only in the encrypted pending state and the encrypted connection.
	ClientSecret string `json:"client_secret,omitempty"`
}

type mcpOAuthPending struct {
	Provider      string   `json:"provider"`
	MCPURL        string   `json:"mcp_url"`
	Target        string   `json:"target"`
	Name          string   `json:"name"`
	ConnectionID  string   `json:"connection_id,omitempty"`
	RedirectURI   string   `json:"redirect_uri"`
	Verifier      string   `json:"verifier"`
	ClientID      string   `json:"client_id"`
	ClientSecret  string   `json:"client_secret,omitempty"`
	Issuer        string   `json:"issuer"`
	TokenEndpoint string   `json:"token_endpoint"`
	Resource      string   `json:"resource,omitempty"`
	Scopes        []string `json:"scopes,omitempty"`
}

func (s *Server) mcpOAuthRedirectURI(r *http.Request) string {
	return s.oauthBaseURL(r) + mcpOAuthCallbackPath
}

func mcpOAuthStateHash(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

// mcpOAuthStateWorkspace extracts the nonsecret workspace selector from a
// state. The selector only chooses which workspace the callback is admitted
// in; the pending row is still bound to that workspace, account and session.
func mcpOAuthStateWorkspace(state string) string {
	workspace, _, ok := strings.Cut(state, ".")
	if !ok || workspace == "" || len(workspace) > 128 {
		return ""
	}
	return workspace
}

// MCPOAuthStartAPI handles POST /api/v1/mcp/oauth/start.
func (s *Server) MCPOAuthStartAPI(w http.ResponseWriter, r *http.Request) {
	pendingStore, ok := s.store.(service.MCPOAuthPendingStorer)
	if !ok {
		httpResponse(w, "MCP OAuth is not supported by this store", http.StatusServiceUnavailable)
		return
	}
	var req mcpOAuthStartRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}
	principal, ok := service.AccessPrincipalFromContext(r.Context())
	if !ok || principal.UserID == "" || principal.SessionID == "" {
		httpResponse(w, "MCP OAuth requires a signed-in browser session", http.StatusForbidden)
		return
	}
	switch req.Target {
	case "", service.ConnectionScopePersonal:
		req.Target = service.ConnectionScopePersonal
	case "shared", service.ConnectionScopeWorkspace:
		req.Target = service.ConnectionScopeWorkspace
		resource := service.AccessResource{WorkspaceID: principal.WorkspaceID}
		if !principal.Allows("connections.write", resource) || !principal.Allows("credentials.manage", resource) {
			httpResponse(w, "connecting a shared account requires connections.write and credentials.manage", http.StatusForbidden)
			return
		}
	default:
		httpResponse(w, "target must be personal or shared", http.StatusBadRequest)
		return
	}

	pending, auth, status, err := s.mcpOAuthStartTarget(r.Context(), req)
	if err != nil {
		httpResponse(w, err.Error(), status)
		return
	}
	pending.Target = req.Target
	pending.RedirectURI = s.mcpOAuthRedirectURI(r)

	client := mcpauth.Client(mcpauth.AllowsPrivate(pending.MCPURL))
	meta, err := mcpauth.Discover(r.Context(), client, pending.MCPURL, auth.AuthorizationServer)
	if err != nil {
		httpResponse(w, "MCP authorization discovery failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	switch {
	case auth.ClientID != "":
		pending.ClientID = auth.ClientID
		if req.ClientSecret != "" {
			pending.ClientSecret = req.ClientSecret
		}
	case pending.ClientID != "" && pending.Issuer == meta.Issuer:
		// Reconnect: reuse the dynamically registered client of the same
		// authorization server instead of registering another one.
	default:
		pending.ClientID, pending.ClientSecret, err = mcpauth.Register(r.Context(), client, meta, pending.RedirectURI, "AT")
		if err != nil {
			status := http.StatusBadGateway
			if errors.Is(err, mcpauth.ErrRegistrationUnsupported) {
				status = http.StatusBadRequest
			}
			httpResponse(w, err.Error(), status)
			return
		}
	}
	pending.Issuer, pending.TokenEndpoint, pending.Resource = meta.Issuer, meta.TokenEndpoint, meta.Resource
	pending.Scopes = auth.Scopes
	if len(pending.Scopes) == 0 {
		pending.Scopes = meta.DefaultScopes()
	}
	pkce, err := mcpauth.NewPKCE()
	if err != nil {
		httpResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	pending.Verifier = pkce.Verifier
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		httpResponse(w, "generate state failed", http.StatusInternalServerError)
		return
	}
	state := principal.WorkspaceID + "." + base64.RawURLEncoding.EncodeToString(nonce[:])
	payload, err := json.Marshal(pending)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := pendingStore.SaveMCPOAuthPending(r.Context(), mcpOAuthStateHash(state), payload); err != nil {
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, err.Error(), http.StatusConflict)
		return
	}
	authorizeURL, err := mcpauth.AuthorizeURL(meta, pending.ClientID, pending.RedirectURI, pending.Scopes, state, pkce.Challenge)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadGateway)
		return
	}
	httpResponseJSON(w, map[string]any{"authorize_url": authorizeURL, "provider": pending.Provider, "redirect_uri": pending.RedirectURI}, http.StatusOK)
}

// mcpOAuthStartTarget resolves which server is being authorized. The
// upstream is read through the execution plane (mcp.use, unredacted), never
// the management DTO, and the request can only name a configured upstream.
func (s *Server) mcpOAuthStartTarget(ctx context.Context, req mcpOAuthStartRequest) (*mcpOAuthPending, service.MCPUpstreamAuth, int, error) {
	refs := 0
	for _, v := range []string{req.SetID, req.ServerID, req.ConnectionID} {
		if v != "" {
			refs++
		}
	}
	if refs != 1 {
		return nil, service.MCPUpstreamAuth{}, http.StatusBadRequest, errors.New("name exactly one of set_id, server_id or connection_id")
	}
	if req.ConnectionID != "" {
		return s.mcpOAuthReconnectTarget(ctx, req)
	}
	runCtx, err := s.bindRuntimePrincipal(ctx, "mcp-oauth")
	if err != nil {
		return nil, service.MCPUpstreamAuth{}, http.StatusForbidden, errors.New("runtime identity unavailable")
	}
	resolver, ok := s.store.(service.WorkspaceCredentialStorer)
	if !ok {
		return nil, service.MCPUpstreamAuth{}, http.StatusServiceUnavailable, errors.New("store not configured")
	}
	var upstreams []service.MCPUpstream
	if req.SetID != "" {
		if s.mcpSetStore == nil {
			return nil, service.MCPUpstreamAuth{}, http.StatusServiceUnavailable, errors.New("store not configured")
		}
		set, err := s.mcpSetStore.GetMCPSet(ctx, req.SetID)
		if err != nil || set == nil {
			return nil, service.MCPUpstreamAuth{}, http.StatusNotFound, errors.New("MCP set not found")
		}
		if err := service.CheckExecution(runCtx, service.ExecutionAction{Kind: "resource", Name: "mcp.use", ResourceID: set.Name}); err != nil {
			return nil, service.MCPUpstreamAuth{}, http.StatusForbidden, errors.New("MCP set use denied")
		}
		full, err := resolver.ResolveMCPSetForUse(runCtx, set.Name)
		if err != nil || full == nil || full.ID != set.ID {
			return nil, service.MCPUpstreamAuth{}, http.StatusNotFound, errors.New("MCP set not found")
		}
		upstreams = full.Config.MCPUpstreams
	} else {
		if s.mcpServerStore == nil {
			return nil, service.MCPUpstreamAuth{}, http.StatusServiceUnavailable, errors.New("store not configured")
		}
		srv, err := s.mcpServerStore.GetMCPServer(ctx, req.ServerID)
		if err != nil || srv == nil {
			return nil, service.MCPUpstreamAuth{}, http.StatusNotFound, errors.New("MCP server not found")
		}
		if err := service.CheckExecution(runCtx, service.ExecutionAction{Kind: "resource", Name: "mcp_servers.use", ResourceID: srv.ID}); err != nil {
			return nil, service.MCPUpstreamAuth{}, http.StatusForbidden, errors.New("MCP server use denied")
		}
		full, err := resolver.ResolveMCPServerForUse(runCtx, srv.Name)
		if err != nil || full == nil || full.ID != srv.ID {
			return nil, service.MCPUpstreamAuth{}, http.StatusNotFound, errors.New("MCP server not found")
		}
		upstreams = full.Config.MCPUpstreams
	}
	if req.UpstreamIndex < 0 || req.UpstreamIndex >= len(upstreams) {
		return nil, service.MCPUpstreamAuth{}, http.StatusBadRequest, errors.New("upstream_index is out of range")
	}
	upstream := []service.MCPUpstream{upstreams[req.UpstreamIndex]}
	if upstream[0].Auth == nil {
		return nil, service.MCPUpstreamAuth{}, http.StatusBadRequest, errors.New("this upstream does not use OAuth; enable OAuth in its Authentication settings first")
	}
	if err := service.NormalizeMCPUpstreamAuth(upstream); err != nil {
		return nil, service.MCPUpstreamAuth{}, http.StatusBadRequest, err
	}
	auth := *upstream[0].Auth
	name := strings.TrimSpace(req.ConnectionName)
	if name == "" {
		name = "MCP account"
	}
	return &mcpOAuthPending{Provider: auth.Provider, MCPURL: strings.TrimSpace(upstream[0].URL), Name: name}, auth, 0, nil
}

// mcpOAuthReconnectTarget re-authorizes an existing MCP connection the caller
// may change, against the server and scopes it was authorized for.
func (s *Server) mcpOAuthReconnectTarget(ctx context.Context, req mcpOAuthStartRequest) (*mcpOAuthPending, service.MCPUpstreamAuth, int, error) {
	if s.connectionStore == nil {
		return nil, service.MCPUpstreamAuth{}, http.StatusServiceUnavailable, errors.New("store not configured")
	}
	conn, err := s.connectionStore.GetConnection(ctx, req.ConnectionID)
	if err != nil || conn == nil {
		return nil, service.MCPUpstreamAuth{}, http.StatusNotFound, errors.New("connection not found")
	}
	principal, _ := service.AccessPrincipalFromContext(ctx)
	wantTarget := service.ConnectionScope(conn.OwnerUserID)
	if conn.OwnerUserID != "" && conn.OwnerUserID != principal.UserID {
		return nil, service.MCPUpstreamAuth{}, http.StatusNotFound, errors.New("connection not found")
	}
	if req.Target != wantTarget {
		return nil, service.MCPUpstreamAuth{}, http.StatusBadRequest, fmt.Errorf("this is a %s connection", wantTarget)
	}
	m := conn.Credentials.MCPOAuth
	if m == nil {
		// Workspace credentials are blanked for readers without
		// credentials.manage, which also cannot change a shared connection.
		return nil, service.MCPUpstreamAuth{}, http.StatusBadRequest, errors.New("this connection holds no MCP authorization you can renew")
	}
	pending := &mcpOAuthPending{
		Provider: conn.Provider, MCPURL: m.MCPURL, Name: conn.Name, ConnectionID: conn.ID,
		ClientID: m.ClientID, ClientSecret: m.ClientSecret, Issuer: m.Issuer,
	}
	return pending, service.MCPUpstreamAuth{Provider: conn.Provider, Scopes: m.Scopes}, 0, nil
}

// MCPOAuthCallbackAPI handles GET /api/v1/mcp/oauth/callback.
func (s *Server) MCPOAuthCallbackAPI(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	render := func(ok bool, message, connectionID string) {
		s.renderMCPOAuthResult(w, ok, message, connectionID, q.Get("state"))
	}
	if code := q.Get("error"); code != "" {
		// The authorization server's description is not shown: it is
		// attacker-influenced text rendered on AT's origin.
		render(false, "The authorization server refused the request ("+mcpOAuthErrorCode(code)+").", "")
		return
	}
	state, code := q.Get("state"), q.Get("code")
	pendingStore, ok := s.store.(service.MCPOAuthPendingStorer)
	if state == "" || code == "" || !ok {
		render(false, "The authorization response is incomplete. Start again.", "")
		return
	}
	raw, err := pendingStore.TakeMCPOAuthPending(r.Context(), mcpOAuthStateHash(state))
	if err != nil || raw == nil {
		render(false, "This authorization expired or belongs to another session. Start again.", "")
		return
	}
	var pending mcpOAuthPending
	if err := json.Unmarshal(raw, &pending); err != nil {
		render(false, "The pending authorization is unreadable. Start again.", "")
		return
	}
	if pending.RedirectURI != s.mcpOAuthRedirectURI(r) {
		render(false, "The authorization returned to a different address than it started from.", "")
		return
	}
	client := mcpauth.Client(mcpauth.AllowsPrivate(pending.MCPURL))
	tok, err := mcpauth.Exchange(r.Context(), client, pending.TokenEndpoint, pending.ClientID, pending.ClientSecret, code, pending.RedirectURI, pending.Verifier, pending.Resource)
	if err != nil {
		slog.Warn("MCP OAuth code exchange failed", "provider", pending.Provider, "error", err)
		render(false, "The authorization server did not accept the code. Start again.", "")
		return
	}
	scopes := pending.Scopes
	if tok.Scope != "" {
		scopes = strings.Fields(tok.Scope)
	}
	cred := &service.MCPOAuthCredential{
		AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken, ExpiresAt: tok.ExpiresAt(time.Now()),
		ClientID: pending.ClientID, ClientSecret: pending.ClientSecret, Issuer: pending.Issuer,
		TokenEndpoint: pending.TokenEndpoint, Resource: pending.Resource, Scopes: scopes, MCPURL: pending.MCPURL,
	}
	conn, err := s.saveMCPOAuthConnection(r, pending, cred)
	if err != nil {
		slog.Warn("MCP OAuth connection save failed", "provider", pending.Provider, "error", err)
		msg := "The account was authorized but could not be saved."
		if errors.Is(err, service.ErrAccessDenied) {
			msg = "You are not allowed to save this connection."
		}
		render(false, msg, "")
		return
	}
	slog.Info("MCP OAuth account connected", "provider", pending.Provider, "connection_id", conn.ID, "scope", conn.Scope)
	render(true, "Account connected. You can close this window.", conn.ID)
}

// saveMCPOAuthConnection creates or renews the connection. A renewal keeps
// the row (and every agent binding pointing at it); a new personal account
// with an existing name replaces that account's authorization.
func (s *Server) saveMCPOAuthConnection(r *http.Request, pending mcpOAuthPending, cred *service.MCPOAuthCredential) (*service.Connection, error) {
	if s.connectionStore == nil {
		return nil, errors.New("store not configured")
	}
	ctx := r.Context()
	principal, _ := service.AccessPrincipalFromContext(ctx)
	owner := ""
	if pending.Target == service.ConnectionScopePersonal {
		owner = principal.UserID
	}
	existingID := pending.ConnectionID
	if existingID == "" {
		list, err := s.connectionStore.ListConnectionsByProvider(ctx, pending.Provider)
		if err != nil {
			return nil, err
		}
		for _, c := range list {
			if c.Name == pending.Name && c.OwnerUserID == owner {
				existingID = c.ID
				break
			}
		}
	}
	by := s.getUserEmail(r)
	label := cred.Issuer
	if u, err := url.Parse(cred.Issuer); err == nil && u.Host != "" {
		label = u.Host
	}
	if existingID != "" {
		existing, err := s.connectionStore.GetConnection(ctx, existingID)
		if err != nil {
			return nil, err
		}
		if existing == nil || existing.OwnerUserID != owner || existing.Provider != pending.Provider {
			return nil, service.ErrAccessDenied
		}
		creds := existing.Credentials
		creds.MCPOAuth = cred
		existing.Credentials = creds
		existing.AccountLabel = label
		existing.UpdatedBy = by
		rec, err := s.connectionStore.UpdateConnection(ctx, existing.ID, *existing)
		if err == nil && rec == nil {
			err = service.ErrAccessResourceNotFound
		}
		return rec, err
	}
	return s.connectionStore.CreateConnection(ctx, service.Connection{
		OwnerUserID: owner, Provider: pending.Provider, Name: pending.Name, AccountLabel: label,
		Description: "MCP OAuth account for " + pending.MCPURL,
		Credentials: service.ConnectionCredentials{MCPOAuth: cred},
		CreatedBy:   by, UpdatedBy: by,
	})
}

func mcpOAuthErrorCode(code string) string {
	for _, r := range code {
		if !(r >= 'a' && r <= 'z' || r == '_') {
			return "error"
		}
	}
	if len(code) > 64 {
		return "error"
	}
	return code
}

// renderMCPOAuthResult answers the popup with a document that reports the
// outcome to the exact opener origin (never "*") and closes itself. A
// state-scoped BroadcastChannel survives authorization servers that sever
// window.opener via Cross-Origin-Opener-Policy.
func (s *Server) renderMCPOAuthResult(w http.ResponseWriter, ok bool, message, connectionID, state string) {
	var nonce [16]byte
	_, _ = rand.Read(nonce[:])
	n := base64.RawStdEncoding.EncodeToString(nonce[:])
	origin := ""
	if s.nativeAuth != nil {
		origin = s.nativeAuth.Origin()
	}
	payload, _ := json.Marshal(map[string]any{"type": "at-mcp-oauth-result", "ok": ok, "message": message, "connection_id": connectionID, "state": state})
	var safe bytes.Buffer
	json.HTMLEscape(&safe, payload)
	originJSON, _ := json.Marshal(origin)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+n+"'")
	status := http.StatusOK
	if !ok {
		status = http.StatusBadRequest
	}
	w.WriteHeader(status)
	fmt.Fprintf(w, `<!DOCTYPE html><html><head><meta charset="utf-8"><title>MCP authorization</title></head><body><p>%s</p>
<script type="application/json" id="result">%s</script>
<script nonce="%s">(function(){var r=JSON.parse(document.getElementById("result").textContent);var o=%s;var sent=false;try{if(r.state&&typeof BroadcastChannel!=="undefined"){var c=new BroadcastChannel("at-mcp-oauth:"+r.state);c.postMessage(r);setTimeout(function(){c.close();},1000);sent=true;}}catch(e){}try{if(window.opener&&o){window.opener.postMessage(r,o);sent=true;}}catch(e){}if(sent){setTimeout(function(){window.close();},%s);}})();</script>
</body></html>`, html.EscapeString(message), safe.String(), n, originJSON, strconv.Itoa(map[bool]int{true: 300, false: 4000}[ok]))
}
