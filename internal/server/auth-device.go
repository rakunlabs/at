package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/antropic"
	"github.com/rakunlabs/at/internal/service/llm/openai"
)

// ─── Provider OAuth Device Flows ───
//
// Used by auth_type:"copilot" for GitHub OAuth and auth_type:"chatgpt" for
// the OpenAI Codex device flow.
//
// Flow:
//   1. UI calls POST /api/v1/providers/device-auth with the provider key
//   2. Backend requests a device code from GitHub
//   3. Backend returns user_code + verification_uri to the UI
//   4. UI shows the code and link; user visits GitHub and authorizes
//   5. Backend polls GitHub until the user authorizes (or timeout)
//   6. Backend saves the OAuth token into the provider's api_key and hot-reloads
//   7. UI polls GET /api/v1/providers/device-auth-status?key=xxx to track progress

const (
	// Client ID used by copilot.vim / copilot.lua — the standard Copilot editor OAuth app.
	copilotOAuthClientID = "Iv1.b507a08c87ecfe98"

	githubDeviceCodeURL  = "https://github.com/login/device/code"
	githubAccessTokenURL = "https://github.com/login/oauth/access_token"
)

// deviceFlowState tracks an in-progress device authorization flow.
type deviceFlowState struct {
	Status   string `json:"status"` // "pending", "authorized", "expired", "error"
	Error    string `json:"error,omitempty"`
	UserCode string `json:"user_code,omitempty"`
}

// deviceFlowManager tracks active device flow sessions per provider key.
type deviceFlowManager struct {
	mu    sync.Mutex
	flows map[providerAuthFlowKey]*deviceFlowState
}

var deviceFlows = &deviceFlowManager{
	flows: make(map[providerAuthFlowKey]*deviceFlowState),
}

func (m *deviceFlowManager) set(key providerAuthFlowKey, state *deviceFlowState) {
	m.mu.Lock()
	m.flows[key] = state
	m.mu.Unlock()
}

func (m *deviceFlowManager) get(key providerAuthFlowKey) *deviceFlowState {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.flows[key]
	if !ok {
		return nil
	}
	return s
}

func (m *deviceFlowManager) remove(key providerAuthFlowKey) {
	m.mu.Lock()
	delete(m.flows, key)
	m.mu.Unlock()
}

// ─── Request / Response types ───

type deviceAuthRequest struct {
	Key string `json:"key"`
}

type deviceAuthResponse struct {
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type deviceAuthStatusResponse struct {
	Status string `json:"status"` // "pending", "authorized", "expired", "error", "none"
	Error  string `json:"error,omitempty"`
}

type codexDeviceProviderSnapshot struct {
	ID                 string
	Type               string
	BaseURL            string
	Proxy              string
	InsecureSkipVerify bool
}

// ─── GitHub API types ───

type githubDeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type githubAccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
	Error       string `json:"error,omitempty"`
}

// ─── Handlers ───

// DeviceAuthAPI handles POST /api/v1/providers/device-auth.
// Initiates the OAuth device flow for a Copilot or ChatGPT provider.
func (s *Server) DeviceAuthAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req deviceAuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Key == "" {
		httpResponse(w, "key is required", http.StatusBadRequest)
		return
	}

	// Load the provider to verify it exists and supports device authorization.
	record, ok := s.providerAuthRecord(w, r, req.Key, "copilot", "chatgpt")
	if !ok {
		return
	}
	flowKey := providerFlowKey(r.Context(), record)

	// Check if there's already a pending flow for this key.
	if existing := deviceFlows.get(flowKey); existing != nil && existing.Status == "pending" {
		httpResponse(w, "a device authorization flow is already in progress for this provider", http.StatusConflict)
		return
	}

	// Request device code from GitHub.
	// Build a proxy-aware HTTP client from the provider config so the device
	// flow can reach github.com through the configured proxy.
	httpClient, err := s.providerAuthHTTPClient(record.Config.Proxy, record.Config.InsecureSkipVerify)
	if err != nil {
		slog.Error("device auth: failed to create proxy client", "error", err)
		httpResponse(w, fmt.Sprintf("failed to create proxy client: %v", err), http.StatusInternalServerError)
		return
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	if record.Config.AuthType == "chatgpt" {
		deviceResp, err := openai.RequestCodexDeviceCode(r.Context(), httpClient)
		if err != nil {
			slog.Error("ChatGPT device auth: request device code failed", "error", err)
			httpResponse(w, fmt.Sprintf("failed to start ChatGPT device flow: %v", err), http.StatusBadGateway)
			return
		}

		deviceFlows.set(flowKey, &deviceFlowState{
			Status:   "pending",
			UserCode: deviceResp.UserCode,
		})
		snapshot := codexDeviceProviderSnapshot{
			ID:                 record.ID,
			Type:               record.Config.Type,
			BaseURL:            record.Config.BaseURL,
			Proxy:              record.Config.Proxy,
			InsecureSkipVerify: record.Config.InsecureSkipVerify,
		}
		go s.pollCodexDeviceAuth(context.WithoutCancel(r.Context()), flowKey, req.Key, snapshot, deviceResp, httpClient)

		httpResponseJSON(w, deviceAuthResponse{
			UserCode:        deviceResp.UserCode,
			VerificationURI: deviceResp.VerificationURL,
			ExpiresIn:       int(openai.CodexDeviceAuthTimeout.Seconds()),
			Interval:        int(deviceResp.Interval.Seconds()),
		}, http.StatusOK)
		return
	}

	deviceResp, err := requestDeviceCode(r.Context(), httpClient)
	if err != nil {
		slog.Error("device auth: request device code failed", "error", err)
		httpResponse(w, fmt.Sprintf("failed to start device flow: %v", err), http.StatusBadGateway)
		return
	}

	// Track the flow as pending.
	state := &deviceFlowState{
		Status:   "pending",
		UserCode: deviceResp.UserCode,
	}
	deviceFlows.set(flowKey, state)

	// Start background polling.
	go s.pollDeviceAuth(context.WithoutCancel(r.Context()), flowKey, req.Key, deviceResp, httpClient)

	httpResponseJSON(w, deviceAuthResponse{
		UserCode:        deviceResp.UserCode,
		VerificationURI: deviceResp.VerificationURI,
		ExpiresIn:       deviceResp.ExpiresIn,
		Interval:        deviceResp.Interval,
	}, http.StatusOK)
}

func (s *Server) pollCodexDeviceAuth(ctx context.Context, flowKey providerAuthFlowKey, providerKey string, snapshot codexDeviceProviderSnapshot, deviceResp *openai.CodexDeviceCode, httpClient *http.Client) {
	ctx, cancel := context.WithTimeout(ctx, openai.CodexDeviceAuthTimeout)
	defer cancel()
	tokens, err := openai.CompleteCodexDeviceAuth(ctx, deviceResp, httpClient)
	if err != nil {
		status := "error"
		message := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			status = "expired"
			message = "device code expired; please try again"
		}
		slog.Error("ChatGPT device auth failed", "key", providerKey, "error", err)
		deviceFlows.set(flowKey, &deviceFlowState{Status: status, Error: message})
		return
	}

	if err := s.saveCodexAuthTokens(ctx, providerKey, snapshot, tokens); err != nil {
		slog.Error("ChatGPT device auth: failed to save tokens", "key", providerKey, "error", err)
		deviceFlows.set(flowKey, &deviceFlowState{Status: "error", Error: "authorized but failed to save tokens: " + err.Error()})
		return
	}

	deviceFlows.set(flowKey, &deviceFlowState{Status: "authorized"})
	go func() {
		time.Sleep(30 * time.Second)
		deviceFlows.remove(flowKey)
	}()
}

func (s *Server) saveCodexAuthTokens(ctx context.Context, providerKey string, snapshot codexDeviceProviderSnapshot, tokens *openai.CodexTokens) error {
	if s.store == nil {
		return fmt.Errorf("store not configured")
	}
	if tokens == nil || tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.AccountID == "" {
		return fmt.Errorf("ChatGPT OAuth response is missing access, refresh, or account credentials")
	}

	record, err := s.store.GetProvider(ctx, providerKey)
	if err != nil {
		return fmt.Errorf("get provider: %w", err)
	}
	if record == nil {
		return fmt.Errorf("provider %q not found", providerKey)
	}
	if record.ID != snapshot.ID || record.Config.Type != snapshot.Type || record.Config.AuthType != "chatgpt" ||
		record.Config.BaseURL != snapshot.BaseURL || record.Config.Proxy != snapshot.Proxy ||
		record.Config.InsecureSkipVerify != snapshot.InsecureSkipVerify {
		return fmt.Errorf("provider changed while ChatGPT authorization was pending; start authorization again")
	}

	cfg := record.Config
	cfg.APIKey = tokens.AccessToken
	cfg.RefreshToken = tokens.RefreshToken
	if !tokens.ExpiresAt.IsZero() {
		cfg.TokenExpiresAt = tokens.ExpiresAt.UTC().Format(time.RFC3339)
	}
	cfg.ExtraHeaders = maps.Clone(cfg.ExtraHeaders)
	if cfg.ExtraHeaders == nil {
		cfg.ExtraHeaders = make(map[string]string)
	}
	cfg.ExtraHeaders["ChatGPT-Account-ID"] = tokens.AccountID

	if _, err := s.store.UpdateProvider(ctx, providerKey, service.ProviderRecord{
		Key:       providerKey,
		Config:    cfg,
		UpdatedBy: "system:oauth",
	}); err != nil {
		return fmt.Errorf("update provider: %w", err)
	}
	if err := s.reloadWorkspaceProvider(ctx, providerKey, cfg); err != nil {
		return fmt.Errorf("reload provider: %w", err)
	}
	return nil
}

// DeviceAuthStatusAPI handles GET /api/v1/providers/device-auth-status?key=xxx.
// Returns the current status of the device authorization flow.
func (s *Server) DeviceAuthStatusAPI(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		httpResponse(w, "key query parameter is required", http.StatusBadRequest)
		return
	}

	record, ok := s.providerAuthRecord(w, r, key, "copilot", "chatgpt")
	if !ok {
		return
	}
	state := deviceFlows.get(providerFlowKey(r.Context(), record))
	if state == nil {
		httpResponseJSON(w, deviceAuthStatusResponse{Status: "none"}, http.StatusOK)
		return
	}

	httpResponseJSON(w, deviceAuthStatusResponse{
		Status: state.Status,
		Error:  state.Error,
	}, http.StatusOK)
}

// ─── Background polling ───

// pollDeviceAuth polls GitHub for the access token in the background.
// On success, it saves the token to the provider config and hot-reloads.
func (s *Server) pollDeviceAuth(ctx context.Context, flowKey providerAuthFlowKey, providerKey string, deviceResp *githubDeviceCodeResponse, httpClient *http.Client) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(deviceResp.ExpiresIn)*time.Second)
	defer cancel()
	interval := time.Duration(deviceResp.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}

	deadline := time.Now().Add(time.Duration(deviceResp.ExpiresIn) * time.Second)

	for {
		select {
		case <-ctx.Done():
			deviceFlows.set(flowKey, &deviceFlowState{Status: "expired", Error: "device code expired — please try again"})
			return
		case <-time.After(interval):
		}

		if time.Now().After(deadline) {
			slog.Warn("device auth: flow expired", "key", providerKey)
			deviceFlows.set(flowKey, &deviceFlowState{Status: "expired", Error: "device code expired — please try again"})
			return
		}

		token, err := pollAccessToken(ctx, deviceResp.DeviceCode, httpClient)
		if err != nil {
			slog.Error("device auth: poll failed", "key", providerKey, "error", err)
			deviceFlows.set(flowKey, &deviceFlowState{Status: "error", Error: err.Error()})
			return
		}

		if token == "" {
			// Still pending — continue polling.
			continue
		}

		// Got the token. Save it to the provider config.
		slog.Debug("device auth: authorized", "key", providerKey)

		if err := s.saveDeviceAuthToken(ctx, providerKey, flowKey.ProviderID, token); err != nil {
			slog.Error("device auth: failed to save token", "key", providerKey, "error", err)
			deviceFlows.set(flowKey, &deviceFlowState{Status: "error", Error: "authorized but failed to save token: " + err.Error()})
			return
		}

		deviceFlows.set(flowKey, &deviceFlowState{Status: "authorized"})

		// Clean up after a short delay so the UI can read the final status.
		go func() {
			time.Sleep(30 * time.Second)
			deviceFlows.remove(flowKey)
		}()

		return
	}
}

// saveDeviceAuthToken updates the provider's api_key with the OAuth token and hot-reloads.
func (s *Server) saveDeviceAuthToken(ctx context.Context, providerKey, providerID, oauthToken string) error {
	if s.store == nil {
		return fmt.Errorf("store not configured")
	}

	// Read current config.
	record, err := s.store.GetProvider(ctx, providerKey)
	if err != nil {
		return fmt.Errorf("get provider: %w", err)
	}
	if record == nil {
		return fmt.Errorf("provider %q not found", providerKey)
	}

	if record.ID != providerID || record.Config.AuthType != "copilot" {
		return fmt.Errorf("provider changed while authorization was pending; start authorization again")
	}
	// Update api_key with the OAuth token.
	cfg := record.Config
	cfg.APIKey = oauthToken

	// Persist.
	if _, err := s.store.UpdateProvider(ctx, providerKey, service.ProviderRecord{
		Key:       providerKey,
		Config:    cfg,
		UpdatedBy: "",
	}); err != nil {
		return fmt.Errorf("update provider: %w", err)
	}

	// Hot-reload the provider so it uses the new token.
	if err := s.reloadWorkspaceProvider(ctx, providerKey, cfg); err != nil {
		return fmt.Errorf("reload provider: %w", err)
	}

	return nil
}

// ─── GitHub API calls ───

// requestDeviceCode calls POST https://github.com/login/device/code.
func requestDeviceCode(ctx context.Context, httpClient *http.Client) (*githubDeviceCodeResponse, error) {
	form := url.Values{
		"client_id": {copilotOAuthClientID},
		"scope":     {"read:user"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubDeviceCodeURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub returned %d: %s", resp.StatusCode, string(body))
	}

	var deviceResp githubDeviceCodeResponse
	if err := json.Unmarshal(body, &deviceResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if deviceResp.DeviceCode == "" || deviceResp.UserCode == "" {
		return nil, fmt.Errorf("GitHub returned empty device/user code")
	}

	return &deviceResp, nil
}

// pollAccessToken calls POST https://github.com/login/oauth/access_token.
// Returns the access token if authorized, empty string if still pending, or error.
func pollAccessToken(ctx context.Context, deviceCode string, httpClient *http.Client) (string, error) {
	form := url.Values{
		"client_id":   {copilotOAuthClientID},
		"device_code": {deviceCode},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubAccessTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp githubAccessTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}

	switch tokenResp.Error {
	case "":
		// Success.
		if tokenResp.AccessToken == "" {
			return "", fmt.Errorf("GitHub returned empty access token")
		}
		return tokenResp.AccessToken, nil

	case "authorization_pending":
		// User hasn't authorized yet — continue polling.
		return "", nil

	case "slow_down":
		// We're polling too fast — the caller's interval will handle the delay.
		return "", nil

	case "expired_token":
		return "", fmt.Errorf("device code expired")

	case "access_denied":
		return "", fmt.Errorf("user denied authorization")

	default:
		return "", fmt.Errorf("GitHub OAuth error: %s", tokenResp.Error)
	}
}

// ─── Claude Code OAuth Flow ───
//
// Used by auth_type:"claude-code" to authenticate via the Anthropic OAuth flow
// (Authorization Code + PKCE) for Claude Pro/Max subscription users.
//
// Flow:
//   1. UI calls POST /api/v1/providers/claude-auth with the provider key
//   2. Backend generates PKCE challenge + auth URL
//   3. Backend returns auth_url to the UI
//   4. UI shows the link; user opens it, authenticates, gets a code on the redirect page
//   5. User pastes the code back into the UI
//   6. UI calls POST /api/v1/providers/claude-auth/callback with the code
//   7. Backend exchanges the code for access+refresh tokens via Anthropic's token endpoint
//   8. Backend saves tokens and hot-reloads the provider

// claudeAuthState tracks a pending Claude OAuth flow (stores the PKCE verifier).
type claudeAuthState struct {
	Verifier  string
	State     string
	ExpiresAt time.Time
}

type providerAuthFlowKey struct {
	WorkspaceID string
	ProviderID  string
	UserID      string
	SessionID   string
}

func providerFlowKey(ctx context.Context, record *service.ProviderRecord) providerAuthFlowKey {
	p, _ := service.AccessPrincipalFromContext(ctx)
	return providerAuthFlowKey{WorkspaceID: record.WorkspaceID, ProviderID: record.ID, UserID: p.UserID, SessionID: p.SessionID}
}

// claudeAuthManager tracks active Claude auth flows per provider key.
type claudeAuthManager struct {
	mu    sync.Mutex
	flows map[providerAuthFlowKey]*claudeAuthState
}

var claudeAuthFlows = &claudeAuthManager{
	flows: make(map[providerAuthFlowKey]*claudeAuthState),
}

func (m *claudeAuthManager) set(key providerAuthFlowKey, state *claudeAuthState) {
	m.mu.Lock()
	m.flows[key] = state
	m.mu.Unlock()
}

func (m *claudeAuthManager) get(key providerAuthFlowKey) *claudeAuthState {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.flows[key]
	if !ok {
		return nil
	}
	return s
}

func (m *claudeAuthManager) remove(key providerAuthFlowKey) {
	m.mu.Lock()
	delete(m.flows, key)
	m.mu.Unlock()
}

// ─── Claude Auth Request / Response types ───

type claudeAuthStartRequest struct {
	Key string `json:"key"`
}

type claudeAuthStartResponse struct {
	AuthURL   string `json:"auth_url"`
	ExpiresIn int    `json:"expires_in"`
}

type claudeAuthCallbackRequest struct {
	Key  string `json:"key"`
	Code string `json:"code"`
}

type claudeAuthCallbackResponse struct {
	Status string `json:"status"` // "authorized"
}

// ─── Claude Auth Handlers ───

// ClaudeAuthStartAPI handles POST /api/v1/providers/claude-auth.
// Initiates the Claude OAuth flow by generating a PKCE challenge and auth URL.
func (s *Server) ClaudeAuthStartAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req claudeAuthStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Key == "" {
		httpResponse(w, "key is required", http.StatusBadRequest)
		return
	}

	// Load the provider to verify it exists and has auth_type=claude-code.
	record, ok := s.providerAuthRecord(w, r, req.Key, "claude-code")
	if !ok {
		return
	}

	// Generate PKCE challenge.
	pkce, err := antropic.GeneratePKCE()
	if err != nil {
		slog.Error("claude auth: failed to generate PKCE", "error", err)
		httpResponse(w, "failed to generate PKCE challenge", http.StatusInternalServerError)
		return
	}

	// Generate random state parameter (32 bytes → 43-char base64url, matching Claude Code CLI).
	stateBuf := make([]byte, 32)
	if _, err := rand.Read(stateBuf); err != nil {
		slog.Error("claude auth: failed to generate state", "error", err)
		httpResponse(w, "failed to generate state", http.StatusInternalServerError)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(stateBuf)

	// Store the PKCE verifier for the callback.
	expiresIn := 600 // 10 minutes
	flowKey := providerFlowKey(r.Context(), record)
	claudeAuthFlows.set(flowKey, &claudeAuthState{
		Verifier:  pkce.Verifier,
		State:     state,
		ExpiresAt: time.Now().Add(time.Duration(expiresIn) * time.Second),
	})

	// Clean up expired flows after timeout.
	go func() {
		time.Sleep(time.Duration(expiresIn+30) * time.Second)
		if flow := claudeAuthFlows.get(flowKey); flow != nil && time.Now().After(flow.ExpiresAt) {
			claudeAuthFlows.remove(flowKey)
		}
	}()

	// Build the authorization URL.
	authURL := antropic.BuildAuthURL(pkce.Challenge, state)
	slog.Info("claude auth: generated auth URL", "url", authURL)

	httpResponseJSON(w, claudeAuthStartResponse{
		AuthURL:   authURL,
		ExpiresIn: expiresIn,
	}, http.StatusOK)
}

// ClaudeAuthCallbackAPI handles POST /api/v1/providers/claude-auth/callback.
// Exchanges the pasted authorization code for OAuth tokens.
func (s *Server) ClaudeAuthCallbackAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req claudeAuthCallbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Key == "" || req.Code == "" {
		httpResponse(w, "key and code are required", http.StatusBadRequest)
		return
	}

	// Resolve the same workspace/provider and account before looking up PKCE.
	record, ok := s.providerAuthRecord(w, r, req.Key, "claude-code")
	if !ok {
		return
	}
	flowKey := providerFlowKey(r.Context(), record)
	flow := claudeAuthFlows.get(flowKey)
	if flow == nil {
		httpResponse(w, "no pending auth flow for this provider (start a new one)", http.StatusBadRequest)
		return
	}

	if time.Now().After(flow.ExpiresAt) {
		claudeAuthFlows.remove(flowKey)
		httpResponse(w, "auth flow expired — please start a new one", http.StatusBadRequest)
		return
	}

	// Build a proxy-aware HTTP client from the provider config.
	httpClient, err := s.providerAuthHTTPClient(record.Config.Proxy, record.Config.InsecureSkipVerify)
	if err != nil {
		slog.Error("claude auth callback: failed to create proxy client", "error", err)
		httpResponse(w, fmt.Sprintf("failed to create proxy client: %v", err), http.StatusInternalServerError)
		return
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	// Exchange the code for tokens.
	slog.Info("claude auth callback: exchanging code",
		"key", req.Key,
		"code_len", len(req.Code),
		"verifier_len", len(flow.Verifier),
		"redirect_uri", antropic.ClaudeManualURI,
		"token_url", antropic.ClaudeTokenURL,
	)
	tokenResp, err := antropic.ExchangeAuthCode(r.Context(), req.Code, flow.Verifier, antropic.ClaudeManualURI, httpClient)
	if err != nil {
		slog.Error("claude auth callback: token exchange failed", "key", req.Key, "error", err)
		httpResponse(w, fmt.Sprintf("token exchange failed: %v", err), http.StatusBadGateway)
		return
	}

	// Clean up the flow state.
	claudeAuthFlows.remove(flowKey)

	// Compute the absolute expiry from the OAuth response so the gateway
	// can refresh proactively. ExpiresIn is in seconds; an empty / zero
	// value means "unknown" — saveClaudeAuthTokens will store an empty
	// string and the token source will refresh on first use.
	expiresAt := ""
	if tokenResp.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}

	// Save the tokens to the provider config.
	if err := s.saveClaudeAuthTokens(r.Context(), req.Key, tokenResp.AccessToken, tokenResp.RefreshToken, expiresAt); err != nil {
		slog.Error("claude auth callback: failed to save tokens", "key", req.Key, "error", err)
		httpResponse(w, fmt.Sprintf("authorized but failed to save tokens: %v", err), http.StatusInternalServerError)
		return
	}

	slog.Info("claude auth: authorized", "key", req.Key, "expires_at", expiresAt)

	httpResponseJSON(w, claudeAuthCallbackResponse{
		Status: "authorized",
	}, http.StatusOK)
}

// ─── Claude Auth Token Paste ───

type claudeAuthTokenRequest struct {
	Key          string `json:"key"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// ClaudeAuthTokenAPI handles POST /api/v1/providers/claude-auth/token.
// Accepts pre-extracted OAuth tokens directly (no PKCE/code exchange needed).
// Users can extract tokens from Claude Code CLI credentials and paste them here.
func (s *Server) ClaudeAuthTokenAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req claudeAuthTokenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Key == "" || req.AccessToken == "" || req.RefreshToken == "" {
		httpResponse(w, "key, access_token, and refresh_token are required", http.StatusBadRequest)
		return
	}

	// Load the provider to verify it exists and has auth_type=claude-code.
	if _, ok := s.providerAuthRecord(w, r, req.Key, "claude-code"); !ok {
		return
	}

	// Save the tokens and hot-reload the provider. The paste flow doesn't
	// know the expiry, so pass an empty string — the OAuth token source
	// will treat the token as expired and refresh on first use, learning
	// the real expiry from the refresh response.
	if err := s.saveClaudeAuthTokens(r.Context(), req.Key, req.AccessToken, req.RefreshToken, ""); err != nil {
		slog.Error("claude auth token: failed to save tokens", "key", req.Key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to save tokens: %v", err), http.StatusInternalServerError)
		return
	}

	slog.Info("claude auth: authorized via token paste", "key", req.Key)

	httpResponseJSON(w, claudeAuthCallbackResponse{
		Status: "authorized",
	}, http.StatusOK)
}

// ─── Claude Auth Sync from Claude Code CLI ───

type claudeAuthSyncRequest struct {
	Key string `json:"key"`
}

type claudeAuthSyncResponse struct {
	Status    string `json:"status"`               // "authorized"
	Source    string `json:"source"`               // where the credentials were found
	ExpiresAt string `json:"expires_at,omitempty"` // RFC3339 token expiry time (if known)
}

// ClaudeAuthSyncAPI handles POST /api/v1/providers/claude-auth/sync.
// Auto-extracts OAuth tokens from Claude Code CLI credentials on the server
// (macOS Keychain or ~/.claude/.credentials.json).
func (s *Server) ClaudeAuthSyncAPI(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return
	}

	var req claudeAuthSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpResponse(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Key == "" {
		httpResponse(w, "key is required", http.StatusBadRequest)
		return
	}

	// Load the provider to verify it exists and has auth_type=claude-code.
	if _, ok := s.providerAuthRecord(w, r, req.Key, "claude-code"); !ok {
		return
	}

	// Try to extract tokens from Claude Code CLI credentials.
	accessToken, refreshToken, source, expiresAt, err := extractClaudeCodeTokens()
	if err != nil {
		slog.Error("claude auth sync: failed to extract tokens", "key", req.Key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to extract Claude Code credentials: %v", err), http.StatusBadRequest)
		return
	}

	// Save the tokens and hot-reload the provider. expiresAt is RFC3339
	// (or empty if the CLI credentials didn't carry an expiry).
	if err := s.saveClaudeAuthTokens(r.Context(), req.Key, accessToken, refreshToken, expiresAt); err != nil {
		slog.Error("claude auth sync: failed to save tokens", "key", req.Key, "error", err)
		httpResponse(w, fmt.Sprintf("failed to save tokens: %v", err), http.StatusInternalServerError)
		return
	}

	slog.Info("claude auth: authorized via CLI sync", "key", req.Key, "source", source, "expires_at", expiresAt)

	httpResponseJSON(w, claudeAuthSyncResponse{
		Status:    "authorized",
		Source:    source,
		ExpiresAt: expiresAt,
	}, http.StatusOK)
}

// extractClaudeCodeTokens tries to read Claude Code OAuth tokens from:
// 1. macOS Keychain (if on Darwin)
// 2. ~/.claude/.credentials.json
// Returns accessToken, refreshToken, source description, expiresAt (RFC3339), and error.
func extractClaudeCodeTokens() (string, string, string, string, error) {
	var credJSON string
	var source string

	// Try macOS Keychain first (only on Darwin).
	if isDarwin() {
		for _, svcName := range []string{
			"Claude Code-credentials",
			"Claude Code credentials",
			"Claude Code",
			"claude-code-credentials",
		} {
			out, err := execCommand("security", "find-generic-password", "-s", svcName, "-w")
			if err == nil && strings.TrimSpace(out) != "" {
				credJSON = strings.TrimSpace(out)
				source = "macOS Keychain (" + svcName + ")"
				break
			}
		}
	}

	// Fall back to credentials file.
	if credJSON == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", "", "", "", fmt.Errorf("cannot determine home directory: %w", err)
		}

		credFile := homeDir + "/.claude/.credentials.json"
		data, err := os.ReadFile(credFile)
		if err != nil {
			if os.IsNotExist(err) {
				return "", "", "", "", fmt.Errorf("no Claude Code credentials found (checked macOS Keychain and %s)", credFile)
			}
			return "", "", "", "", fmt.Errorf("failed to read %s: %w", credFile, err)
		}

		credJSON = string(data)
		source = credFile
	}

	// Parse the JSON to extract tokens.
	accessToken, refreshToken, expiresAt, err := parseClaudeCredentials(credJSON)
	if err != nil {
		return "", "", "", "", fmt.Errorf("failed to parse credentials from %s: %w", source, err)
	}

	return accessToken, refreshToken, source, expiresAt, nil
}

// parseClaudeCredentials extracts accessToken, refreshToken, and expiresAt from
// Claude Code's credential JSON. The JSON structure can vary:
//
//	{"claudeAiOauth": {"accessToken": "...", "refreshToken": "...", "expiresAt": 1234567890000}}
//	{"oauth": {"accessToken": "...", "refreshToken": "..."}}
//	{"accessToken": "...", "refreshToken": "..."}
//
// Returns accessToken, refreshToken, expiresAt (RFC3339, empty if unknown), error.
func parseClaudeCredentials(jsonStr string) (string, string, string, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return "", "", "", fmt.Errorf("invalid JSON: %w", err)
	}

	// Try nested keys first, then fall back to top-level.
	var oauthData map[string]any
	for _, key := range []string{"claudeAiOauth", "oauth"} {
		if v, ok := raw[key]; ok {
			if err := json.Unmarshal(v, &oauthData); err == nil {
				break
			}
		}
	}

	// If no nested key found, parse the whole object.
	if oauthData == nil {
		if err := json.Unmarshal([]byte(jsonStr), &oauthData); err != nil {
			return "", "", "", fmt.Errorf("cannot parse credentials: %w", err)
		}
	}

	// Extract tokens — try multiple field names.
	accessToken := firstString(oauthData, "accessToken", "access_token", "access")
	refreshToken := firstString(oauthData, "refreshToken", "refresh_token", "refresh")

	if accessToken == "" || refreshToken == "" {
		return "", "", "", fmt.Errorf("could not find accessToken and refreshToken in credentials")
	}

	// Extract expiresAt — Claude Code stores it as Unix milliseconds.
	var expiresAt string
	for _, key := range []string{"expiresAt", "expires_at", "expires"} {
		if v, ok := oauthData[key]; ok {
			switch ev := v.(type) {
			case float64:
				// Unix milliseconds → RFC3339.
				if ev > 1e12 {
					// Milliseconds.
					expiresAt = time.UnixMilli(int64(ev)).UTC().Format(time.RFC3339)
				} else {
					// Seconds.
					expiresAt = time.Unix(int64(ev), 0).UTC().Format(time.RFC3339)
				}
			case string:
				expiresAt = ev
			}
			if expiresAt != "" {
				break
			}
		}
	}

	return accessToken, refreshToken, expiresAt, nil
}

// firstString returns the first non-empty string value from a map, trying keys in order.
func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// isDarwin returns true if the current OS is macOS.
func isDarwin() bool {
	return runtime.GOOS == "darwin"
}

// execCommand runs a command and returns its stdout as a string.
func execCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// saveClaudeAuthTokens updates the provider's tokens and hot-reloads.
// expiresAt is the access-token expiry as RFC3339 (empty = unknown).
func (s *Server) saveClaudeAuthTokens(ctx context.Context, providerKey, accessToken, refreshToken, expiresAt string) error {
	if s.store == nil {
		return fmt.Errorf("store not configured")
	}

	// Preserve the authenticated workspace/session through the write, even if
	// the HTTP client disconnects after its one-use authorization code is consumed.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	record, err := s.store.GetProvider(ctx, providerKey)
	if err != nil {
		return fmt.Errorf("get provider: %w", err)
	}
	if record == nil {
		return fmt.Errorf("provider %q not found", providerKey)
	}
	if record.Config.AuthType != "claude-code" {
		return fmt.Errorf("provider authentication type changed; start a new authorization")
	}

	cfg := record.Config
	cfg.APIKey = accessToken
	cfg.RefreshToken = refreshToken
	cfg.TokenExpiresAt = expiresAt

	updated, err := s.store.UpdateProvider(ctx, providerKey, service.ProviderRecord{
		Key:       providerKey,
		Config:    cfg,
		UpdatedBy: "",
	})
	if err != nil {
		return fmt.Errorf("update provider: %w", err)
	}
	if updated == nil {
		return fmt.Errorf("provider disappeared during authorization")
	}

	if err := s.reloadWorkspaceProvider(ctx, providerKey, cfg); err != nil {
		return fmt.Errorf("reload provider: %w", err)
	}

	return nil
}

// providerAuthRecord applies the same selected-workspace admission as provider
// CRUD before exchanging a one-use code or reading credentials from the host.
func (s *Server) providerAuthRecord(w http.ResponseWriter, r *http.Request, key string, authTypes ...string) (*service.ProviderRecord, bool) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return nil, false
	}
	record, err := s.store.GetProvider(r.Context(), key)
	if err != nil {
		if !workspaceBusinessError(w, err) {
			slog.Error("claude auth: get provider failed", "key", key, "error", err)
			httpResponse(w, "failed to get provider", http.StatusInternalServerError)
		}
		return nil, false
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("provider %q not found", key), http.StatusNotFound)
		return nil, false
	}
	if actor, ok := service.AccessPrincipalFromContext(r.Context()); ok {
		resource := service.AccessResource{WorkspaceID: record.WorkspaceID, ID: record.ID}
		if !actor.Allows("providers.write", resource) || !actor.Allows("credentials.manage", resource) {
			httpResponse(w, "provider credential management access required", http.StatusForbidden)
			return nil, false
		}
	}
	for _, authType := range authTypes {
		if record.Config.AuthType == authType {
			return record, true
		}
	}
	httpResponse(w, "Save this provider with the selected authentication type before authorizing it.", http.StatusBadRequest)
	return nil, false
}

func (s *Server) providerAuthHTTPClient(proxy string, insecure bool) (*http.Client, error) {
	if s.providerAuthClientFactory != nil {
		return s.providerAuthClientFactory(proxy, insecure)
	}
	return openai.ProxyHTTPClient(proxy, insecure)
}

// claudeOAuthRefreshCallback returns a TokenRefreshCallback that persists
// rotated access/refresh tokens (and the new expiry) for the given provider
// key. Rotation uses the previously loaded refresh credential and the provider's
// authoritative workspace, independent of browser/request lifetimes.
func (s *Server) claudeOAuthRefreshCallback(providerKey string, workspaces ...string) antropic.TokenRefreshCallback {
	workspace := "legacy-default"
	if len(workspaces) > 0 {
		workspace = workspaces[0]
	}
	return func(parent context.Context, previousRefresh, accessToken, refreshToken string, expiresAt time.Time) error {
		store, ok := s.store.(service.ClaudeOAuthTokenStorer)
		if !ok {
			return fmt.Errorf("OAuth token rotation store unavailable")
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
		defer cancel()
		if err := store.RotateClaudeOAuthTokens(ctx, workspace, providerKey, previousRefresh, accessToken, refreshToken, expiresAt); err != nil {
			slog.Error("claude oauth refresh: failed to persist rotated tokens", "key", providerKey, "workspace_id", workspace, "error", err)
			return err
		}
		slog.Info("claude oauth: tokens rotated and persisted", "key", providerKey, "workspace_id", workspace, "expires_at", expiresAt)
		return nil
	}
}

// wireClaudeOAuthCallback installs the persistence callback on the provider's
// OAuth token source if it is using one. Safe to call on any provider type;
// it is a no-op for non-OAuth providers.
func (s *Server) wireClaudeOAuthCallback(providerKey string, p service.LLMProvider, workspace ...string) {
	ap, ok := p.(*antropic.Provider)
	if !ok {
		return
	}
	ap.SetTokenRefreshCallback(s.claudeOAuthRefreshCallback(providerKey, workspace...))
}

func (s *Server) chatGPTOAuthRefreshCallback(providerKey string) openai.CodexTokenRefreshCallback {
	return func(_ context.Context, accessToken, refreshToken, accountID string, expiresAt time.Time) error {
		if s.store == nil {
			return fmt.Errorf("store not configured")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		record, err := s.store.GetProvider(ctx, providerKey)
		if err != nil {
			return fmt.Errorf("read provider: %w", err)
		}
		if record == nil {
			return fmt.Errorf("provider disappeared during refresh")
		}
		if record.Config.AuthType != "chatgpt" {
			return fmt.Errorf("provider auth type changed during refresh")
		}
		storedAccountID := record.Config.ExtraHeaders["ChatGPT-Account-ID"]
		if storedAccountID != "" && accountID != "" && storedAccountID != accountID {
			return fmt.Errorf("provider ChatGPT account changed during refresh")
		}

		cfg := record.Config
		cfg.APIKey = accessToken
		cfg.RefreshToken = refreshToken
		if !expiresAt.IsZero() {
			cfg.TokenExpiresAt = expiresAt.UTC().Format(time.RFC3339)
		}
		if accountID != "" {
			cfg.ExtraHeaders = maps.Clone(cfg.ExtraHeaders)
			if cfg.ExtraHeaders == nil {
				cfg.ExtraHeaders = make(map[string]string)
			}
			cfg.ExtraHeaders["ChatGPT-Account-ID"] = accountID
		}

		if _, err := s.store.UpdateProvider(ctx, providerKey, service.ProviderRecord{
			Key:       providerKey,
			Config:    cfg,
			UpdatedBy: "system:oauth-refresh",
		}); err != nil {
			return fmt.Errorf("persist rotated tokens: %w", err)
		}
		return nil
	}
}

func (s *Server) chatGPTOAuthReloadCallback(providerKey string) openai.CodexTokenReloadCallback {
	return func(ctx context.Context) (string, string, string, time.Time, error) {
		if s.store == nil {
			return "", "", "", time.Time{}, fmt.Errorf("store not configured")
		}
		record, err := s.store.GetProvider(ctx, providerKey)
		if err != nil {
			return "", "", "", time.Time{}, fmt.Errorf("read provider: %w", err)
		}
		if record == nil || record.Config.AuthType != "chatgpt" {
			return "", "", "", time.Time{}, fmt.Errorf("ChatGPT provider no longer exists")
		}
		var expiresAt time.Time
		if record.Config.TokenExpiresAt != "" {
			expiresAt, err = time.Parse(time.RFC3339, record.Config.TokenExpiresAt)
			if err != nil {
				return "", "", "", time.Time{}, fmt.Errorf("parse token expiry: %w", err)
			}
		}
		return record.Config.APIKey, record.Config.RefreshToken,
			record.Config.ExtraHeaders["ChatGPT-Account-ID"], expiresAt, nil
	}
}

func (s *Server) wireChatGPTOAuthCallback(providerKey string, p service.LLMProvider) {
	cp, ok := p.(*openai.CodexProvider)
	if !ok {
		return
	}
	cp.SetTokenRefreshCallback(s.chatGPTOAuthRefreshCallback(providerKey))
	cp.SetTokenReloadCallback(s.chatGPTOAuthReloadCallback(providerKey))
}
