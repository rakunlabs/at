package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/llm/antropic"
	"github.com/rakunlabs/at/internal/service/llm/openai"
)

// ─── GitHub OAuth Device Flow ───
//
// Used by auth_type:"copilot" to authenticate via the GitHub OAuth device flow
// instead of requiring users to manually create a PAT with specific permissions.
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
	flows map[string]*deviceFlowState
}

var deviceFlows = &deviceFlowManager{
	flows: make(map[string]*deviceFlowState),
}

func (m *deviceFlowManager) set(key string, state *deviceFlowState) {
	m.mu.Lock()
	m.flows[key] = state
	m.mu.Unlock()
}

func (m *deviceFlowManager) get(key string) *deviceFlowState {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.flows[key]
	if !ok {
		return nil
	}
	return s
}

func (m *deviceFlowManager) remove(key string) {
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
// Initiates the GitHub OAuth device flow for a Copilot provider.
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

	// Load the provider to verify it exists and has auth_type=copilot.
	record, err := s.store.GetProvider(r.Context(), req.Key)
	if err != nil {
		slog.Error("device auth: get provider failed", "key", req.Key, "error", err)
		httpResponse(w, "failed to get provider", http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("provider %q not found", req.Key), http.StatusNotFound)
		return
	}
	if record.Config.AuthType != "copilot" {
		httpResponse(w, "device auth is only supported for auth_type \"copilot\"", http.StatusBadRequest)
		return
	}

	// Check if there's already a pending flow for this key.
	if existing := deviceFlows.get(req.Key); existing != nil && existing.Status == "pending" {
		httpResponse(w, "a device authorization flow is already in progress for this provider", http.StatusConflict)
		return
	}

	// Request device code from GitHub.
	// Build a proxy-aware HTTP client from the provider config so the device
	// flow can reach github.com through the configured proxy.
	httpClient, err := openai.ProxyHTTPClient(record.Config.Proxy, record.Config.InsecureSkipVerify)
	if err != nil {
		slog.Error("device auth: failed to create proxy client", "error", err)
		httpResponse(w, fmt.Sprintf("failed to create proxy client: %v", err), http.StatusInternalServerError)
		return
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
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
	deviceFlows.set(req.Key, state)

	// Start background polling.
	go s.pollDeviceAuth(req.Key, deviceResp, httpClient)

	httpResponseJSON(w, deviceAuthResponse{
		UserCode:        deviceResp.UserCode,
		VerificationURI: deviceResp.VerificationURI,
		ExpiresIn:       deviceResp.ExpiresIn,
		Interval:        deviceResp.Interval,
	}, http.StatusOK)
}

// DeviceAuthStatusAPI handles GET /api/v1/providers/device-auth-status?key=xxx.
// Returns the current status of the device authorization flow.
func (s *Server) DeviceAuthStatusAPI(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		httpResponse(w, "key query parameter is required", http.StatusBadRequest)
		return
	}

	state := deviceFlows.get(key)
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
func (s *Server) pollDeviceAuth(providerKey string, deviceResp *githubDeviceCodeResponse, httpClient *http.Client) {
	interval := time.Duration(deviceResp.Interval) * time.Second
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}

	deadline := time.Now().Add(time.Duration(deviceResp.ExpiresIn) * time.Second)

	for {
		time.Sleep(interval)

		if time.Now().After(deadline) {
			slog.Warn("device auth: flow expired", "key", providerKey)
			deviceFlows.set(providerKey, &deviceFlowState{Status: "expired", Error: "device code expired — please try again"})
			return
		}

		token, err := pollAccessToken(context.Background(), deviceResp.DeviceCode, httpClient)
		if err != nil {
			slog.Error("device auth: poll failed", "key", providerKey, "error", err)
			deviceFlows.set(providerKey, &deviceFlowState{Status: "error", Error: err.Error()})
			return
		}

		if token == "" {
			// Still pending — continue polling.
			continue
		}

		// Got the token. Save it to the provider config.
		slog.Debug("device auth: authorized", "key", providerKey)

		if err := s.saveDeviceAuthToken(providerKey, token); err != nil {
			slog.Error("device auth: failed to save token", "key", providerKey, "error", err)
			deviceFlows.set(providerKey, &deviceFlowState{Status: "error", Error: "authorized but failed to save token: " + err.Error()})
			return
		}

		deviceFlows.set(providerKey, &deviceFlowState{Status: "authorized"})

		// Clean up after a short delay so the UI can read the final status.
		go func() {
			time.Sleep(30 * time.Second)
			deviceFlows.remove(providerKey)
		}()

		return
	}
}

// saveDeviceAuthToken updates the provider's api_key with the OAuth token and hot-reloads.
func (s *Server) saveDeviceAuthToken(providerKey, oauthToken string) error {
	if s.store == nil {
		return fmt.Errorf("store not configured")
	}

	// Read current config.
	record, err := s.store.GetProvider(context.Background(), providerKey)
	if err != nil {
		return fmt.Errorf("get provider: %w", err)
	}
	if record == nil {
		return fmt.Errorf("provider %q not found", providerKey)
	}

	// Update api_key with the OAuth token.
	cfg := record.Config
	cfg.APIKey = oauthToken

	// Persist.
	if _, err := s.store.UpdateProvider(context.Background(), providerKey, service.ProviderRecord{
		Key:       providerKey,
		Config:    cfg,
		UpdatedBy: "",
	}); err != nil {
		return fmt.Errorf("update provider: %w", err)
	}

	// Hot-reload the provider so it uses the new token.
	if err := s.reloadProvider(providerKey, cfg); err != nil {
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

// claudeAuthManager tracks active Claude auth flows per provider key.
type claudeAuthManager struct {
	mu    sync.Mutex
	flows map[string]*claudeAuthState
}

var claudeAuthFlows = &claudeAuthManager{
	flows: make(map[string]*claudeAuthState),
}

func (m *claudeAuthManager) set(key string, state *claudeAuthState) {
	m.mu.Lock()
	m.flows[key] = state
	m.mu.Unlock()
}

func (m *claudeAuthManager) get(key string) *claudeAuthState {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.flows[key]
	if !ok {
		return nil
	}
	return s
}

func (m *claudeAuthManager) remove(key string) {
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
	record, err := s.store.GetProvider(r.Context(), req.Key)
	if err != nil {
		slog.Error("claude auth: get provider failed", "key", req.Key, "error", err)
		httpResponse(w, "failed to get provider", http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("provider %q not found", req.Key), http.StatusNotFound)
		return
	}
	if record.Config.AuthType != "claude-code" {
		httpResponse(w, "provider auth_type is not \"claude-code\" — save the provider with auth_type set to \"claude-code\" first", http.StatusBadRequest)
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
	claudeAuthFlows.set(req.Key, &claudeAuthState{
		Verifier:  pkce.Verifier,
		State:     state,
		ExpiresAt: time.Now().Add(time.Duration(expiresIn) * time.Second),
	})

	// Clean up expired flows after timeout.
	go func() {
		time.Sleep(time.Duration(expiresIn+30) * time.Second)
		if flow := claudeAuthFlows.get(req.Key); flow != nil && time.Now().After(flow.ExpiresAt) {
			claudeAuthFlows.remove(req.Key)
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

	// Retrieve the stored PKCE verifier.
	flow := claudeAuthFlows.get(req.Key)
	if flow == nil {
		httpResponse(w, "no pending auth flow for this provider (start a new one)", http.StatusBadRequest)
		return
	}

	if time.Now().After(flow.ExpiresAt) {
		claudeAuthFlows.remove(req.Key)
		httpResponse(w, "auth flow expired — please start a new one", http.StatusBadRequest)
		return
	}

	// Build a proxy-aware HTTP client from the provider config.
	record, err := s.store.GetProvider(r.Context(), req.Key)
	if err != nil {
		slog.Error("claude auth callback: get provider failed", "key", req.Key, "error", err)
		httpResponse(w, "failed to get provider", http.StatusInternalServerError)
		return
	}
	if record == nil {
		httpResponse(w, fmt.Sprintf("provider %q not found", req.Key), http.StatusNotFound)
		return
	}

	httpClient, err := openai.ProxyHTTPClient(record.Config.Proxy, record.Config.InsecureSkipVerify)
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
	claudeAuthFlows.remove(req.Key)

	// Save the tokens to the provider config.
	if err := s.saveClaudeAuthTokens(req.Key, tokenResp.AccessToken, tokenResp.RefreshToken); err != nil {
		slog.Error("claude auth callback: failed to save tokens", "key", req.Key, "error", err)
		httpResponse(w, fmt.Sprintf("authorized but failed to save tokens: %v", err), http.StatusInternalServerError)
		return
	}

	slog.Info("claude auth: authorized", "key", req.Key)

	httpResponseJSON(w, claudeAuthCallbackResponse{
		Status: "authorized",
	}, http.StatusOK)
}

// saveClaudeAuthTokens updates the provider's tokens and hot-reloads.
func (s *Server) saveClaudeAuthTokens(providerKey, accessToken, refreshToken string) error {
	if s.store == nil {
		return fmt.Errorf("store not configured")
	}

	record, err := s.store.GetProvider(context.Background(), providerKey)
	if err != nil {
		return fmt.Errorf("get provider: %w", err)
	}
	if record == nil {
		return fmt.Errorf("provider %q not found", providerKey)
	}

	cfg := record.Config
	cfg.APIKey = accessToken
	cfg.RefreshToken = refreshToken

	if _, err := s.store.UpdateProvider(context.Background(), providerKey, service.ProviderRecord{
		Key:       providerKey,
		Config:    cfg,
		UpdatedBy: "",
	}); err != nil {
		return fmt.Errorf("update provider: %w", err)
	}

	if err := s.reloadProvider(providerKey, cfg); err != nil {
		return fmt.Errorf("reload provider: %w", err)
	}

	return nil
}
