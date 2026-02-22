package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
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
	deviceResp, err := requestDeviceCode(r.Context())
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
	go s.pollDeviceAuth(req.Key, deviceResp)

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
func (s *Server) pollDeviceAuth(providerKey string, deviceResp *githubDeviceCodeResponse) {
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

		token, err := pollAccessToken(context.Background(), deviceResp.DeviceCode)
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
		slog.Info("device auth: authorized", "key", providerKey)

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
	if _, err := s.store.UpdateProvider(context.Background(), providerKey, cfg); err != nil {
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
func requestDeviceCode(ctx context.Context) (*githubDeviceCodeResponse, error) {
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

	resp, err := http.DefaultClient.Do(req)
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
func pollAccessToken(ctx context.Context, deviceCode string) (string, error) {
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

	resp, err := http.DefaultClient.Do(req)
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
