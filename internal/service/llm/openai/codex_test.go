package openai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func codexTestJWT(t *testing.T, accountID string, expiresAt time.Time) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, err := json.Marshal(map[string]any{
		"exp": expiresAt.Unix(),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": accountID,
		},
	})
	if err != nil {
		t.Fatalf("marshal JWT payload: %v", err)
	}
	return header + "." + base64.RawURLEncoding.EncodeToString(payload) + ".signature"
}

func codexTestEndpoints(serverURL string) CodexAuthEndpoints {
	return CodexAuthEndpoints{
		DeviceUserCodeURL: serverURL + "/api/accounts/deviceauth/usercode",
		DeviceTokenURL:    serverURL + "/api/accounts/deviceauth/token",
		DeviceCallbackURL: serverURL + "/deviceauth/callback",
		OAuthTokenURL:     serverURL + "/oauth/token",
		VerificationURL:   serverURL + "/codex/device",
	}
}

func TestCodexDeviceAuthFlow(t *testing.T) {
	var polls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode user-code request: %v", err)
			}
			if body["client_id"] != CodexOAuthClientID {
				t.Errorf("client_id = %q", body["client_id"])
			}
			_, _ = w.Write([]byte(`{"device_auth_id":"device-1","usercode":"ABCD-EFGH","interval":"0"}`))
		case "/api/accounts/deviceauth/token":
			polls.Add(1)
			_, _ = w.Write([]byte(`{"authorization_code":"auth-code","code_challenge":"challenge","code_verifier":"verifier"}`))
		case "/oauth/token":
			if got := r.Header.Get("Content-Type"); got != "application/x-www-form-urlencoded" {
				t.Errorf("Content-Type = %q", got)
			}
			if err := r.ParseForm(); err != nil {
				t.Errorf("parse token form: %v", err)
			}
			want := url.Values{
				"grant_type":    {"authorization_code"},
				"code":          {"auth-code"},
				"redirect_uri":  {server.URL + "/deviceauth/callback"},
				"client_id":     {CodexOAuthClientID},
				"code_verifier": {"verifier"},
			}
			for key := range want {
				if r.Form.Get(key) != want.Get(key) {
					t.Errorf("form[%s] = %q, want %q", key, r.Form.Get(key), want.Get(key))
				}
			}
			idToken := codexTestJWT(t, "account-device", time.Now().Add(time.Hour))
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id_token":      idToken,
				"access_token":  "access-device",
				"refresh_token": "refresh-device",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	endpoints := codexTestEndpoints(server.URL)
	device, err := RequestCodexDeviceCode(context.Background(), server.Client(), endpoints)
	if err != nil {
		t.Fatalf("RequestCodexDeviceCode: %v", err)
	}
	if device.UserCode != "ABCD-EFGH" || device.DeviceAuthID != "device-1" || device.Interval != time.Second {
		t.Fatalf("unexpected device code: %+v", device)
	}
	tokens, err := CompleteCodexDeviceAuth(context.Background(), device, server.Client(), endpoints)
	if err != nil {
		t.Fatalf("CompleteCodexDeviceAuth: %v", err)
	}
	if tokens.AccessToken != "access-device" || tokens.RefreshToken != "refresh-device" {
		t.Fatalf("unexpected tokens: %+v", tokens)
	}
	if tokens.AccountID != "account-device" {
		t.Fatalf("AccountID = %q", tokens.AccountID)
	}
	if polls.Load() != 1 {
		t.Fatalf("poll count = %d, want 1", polls.Load())
	}
}

func TestCodexTokenSourceRefreshesJSONAndCallsCallback(t *testing.T) {
	newAccessToken := codexTestJWT(t, "account-old", time.Now().Add(time.Hour))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode refresh request: %v", err)
		}
		if body["client_id"] != CodexOAuthClientID || body["grant_type"] != "refresh_token" || body["refresh_token"] != "refresh-old" {
			t.Errorf("unexpected refresh request: %#v", body)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  newAccessToken,
			"refresh_token": "refresh-new",
		})
	}))
	defer server.Close()

	source := NewCodexTokenSource("access-old", "refresh-old", "account-old", time.Now().Add(-time.Minute), server.Client(), codexTestEndpoints(server.URL))
	var callbackCalled bool
	source.SetRefreshCallback(func(_ context.Context, accessToken, refreshToken, accountID string, expiresAt time.Time) error {
		callbackCalled = true
		if accessToken != newAccessToken || refreshToken != "refresh-new" || accountID != "account-old" || expiresAt.IsZero() {
			t.Errorf("unexpected callback values: access=%q refresh=%q account=%q expires=%v", accessToken, refreshToken, accountID, expiresAt)
		}
		return nil
	})

	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if token != newAccessToken {
		t.Fatalf("Token = %q", token)
	}
	if source.AccountID() != "account-old" {
		t.Fatalf("AccountID = %q", source.AccountID())
	}
	if !callbackCalled {
		t.Fatal("refresh callback was not called")
	}
}

func TestCodexTokenSourcePreservesSelectedAccountOnRefresh(t *testing.T) {
	newAccessToken := codexTestJWT(t, "account-new", time.Now().Add(time.Hour))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  newAccessToken,
			"refresh_token": "refresh-new",
		})
	}))
	defer server.Close()

	source := NewCodexTokenSource("access-old", "refresh-old", "account-old", time.Now().Add(-time.Minute), server.Client(), codexTestEndpoints(server.URL))
	if _, err := source.Token(context.Background()); err != nil {
		t.Fatalf("Token: %v", err)
	}
	if source.AccountID() != "account-old" {
		t.Fatalf("selected account changed during refresh: %q", source.AccountID())
	}
}

func TestCodexTokenSourceRetriesFailedPersistence(t *testing.T) {
	newAccessToken := codexTestJWT(t, "account-old", time.Now().Add(time.Hour))
	var refreshCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		refreshCalls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"access_token":  newAccessToken,
			"refresh_token": "refresh-new",
		})
	}))
	defer server.Close()

	source := NewCodexTokenSource("access-old", "refresh-old", "account-old", time.Now().Add(-time.Minute), server.Client(), codexTestEndpoints(server.URL))
	var persistCalls atomic.Int32
	source.SetRefreshCallback(func(context.Context, string, string, string, time.Time) error {
		if persistCalls.Add(1) == 1 {
			return errors.New("database unavailable")
		}
		return nil
	})
	if _, err := source.Token(context.Background()); err == nil || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("first Token error = %v", err)
	}
	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if token != newAccessToken || refreshCalls.Load() != 1 || persistCalls.Load() != 2 {
		t.Fatalf("token=%q refresh_calls=%d persist_calls=%d", token, refreshCalls.Load(), persistCalls.Load())
	}
}

func TestCodexTokenSourceReloadsPersistedCredentials(t *testing.T) {
	source := NewCodexTokenSource("access-old", "refresh-old", "account", time.Now().Add(time.Hour), nil)
	source.SetReloadCallback(func(context.Context) (string, string, string, time.Time, error) {
		return "access-new", "refresh-new", "account", time.Now().Add(2 * time.Hour), nil
	})
	reloaded, err := source.Reload(context.Background())
	if err != nil || !reloaded {
		t.Fatalf("Reload: reloaded=%v err=%v", reloaded, err)
	}
	token, err := source.Token(context.Background())
	if err != nil || token != "access-new" {
		t.Fatalf("Token = %q, err = %v", token, err)
	}
}

func TestCodexTokenSourceKeepsAccessTokenWhenRefreshOmitsIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"refresh_token": "refresh-new"})
	}))
	defer server.Close()

	source := NewCodexTokenSource("access-old", "refresh-old", "account-old", time.Now().Add(-time.Minute), server.Client(), codexTestEndpoints(server.URL))
	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	if token != "access-old" || source.AccountID() != "account-old" {
		t.Fatalf("refresh lost existing credentials: token=%q account=%q", token, source.AccountID())
	}
}

func TestCodexProviderChatConvertsRequestAndSSE(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-static" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("ChatGPT-Account-ID"); got != "account-static" {
			t.Errorf("ChatGPT-Account-ID = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		events := []string{
			`{"type":"response.created","response":{"id":"response-1"}}`,
			`{"type":"response.reasoning_summary_text.delta","delta":"checking"}`,
			`{"type":"response.output_text.delta","delta":"answer"}`,
			`{"type":"response.output_item.done","item":{"type":"reasoning","id":"reasoning-1","summary":[],"encrypted_content":"opaque-state"}}`,
			`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call-2","name":"lookup","arguments":"{\"query\":\"go\"}"}}`,
			`{"type":"response.completed","response":{"id":"response-1","usage":{"input_tokens":20,"input_tokens_details":{"cached_tokens":5,"cache_write_tokens":2},"output_tokens":7,"output_tokens_details":{"reasoning_tokens":3},"total_tokens":27}}}`,
		}
		for _, event := range events {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
		}
	}))
	defer server.Close()

	source := NewCodexTokenSource("access-static", "", "account-static", time.Time{}, nil)
	provider := NewCodexProvider("codex-default", "", source,
		WithCodexBaseURL(server.URL+"/backend-api/codex/responses"),
		WithCodexHTTPClient(server.Client()),
	)
	parallel := false
	response, err := provider.Chat(context.Background(), "codex-override", []service.Message{
		{Role: "system", Content: "be precise"},
		{Role: "assistant", Content: []service.ContentBlock{
			{Type: "text", Text: "calling"},
			{Type: "tool_use", ID: "call-1", Name: "lookup", Input: map[string]any{"query": "old"}},
		}},
		{Role: "user", Content: []service.ContentBlock{{Type: "tool_result", ToolUseID: "call-1", Content: "old-result"}}},
	}, []service.Tool{{
		Name: "lookup", Description: "look up data", InputSchema: map[string]any{"type": "object"},
	}}, &service.ChatOptions{
		ReasoningEffort:   "high",
		ParallelToolCalls: &parallel,
		ToolChoice: map[string]any{
			"type": "function", "function": map[string]any{"name": "lookup"},
		},
		ResponseFormat: map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "result", "strict": true, "schema": map[string]any{"type": "object"}},
		},
		ExtraBody: map[string]any{"custom": "value", "stream": false, "store": true},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if response.Content != "answer" || response.ReasoningContent != "checking" {
		t.Fatalf("unexpected response content: %+v", response)
	}
	if response.FinishReason != "tool_calls" || response.Finished || len(response.ToolCalls) != 1 {
		t.Fatalf("unexpected tool response: %+v", response)
	}
	if response.ToolCalls[0].ID != "call-2" || response.ToolCalls[0].Name != "lookup" || response.ToolCalls[0].Arguments["query"] != "go" {
		t.Fatalf("unexpected tool call: %+v", response.ToolCalls[0])
	}
	if !strings.Contains(response.ToolCalls[0].ThoughtSignature, `"encrypted_content":"opaque-state"`) {
		t.Fatalf("missing encrypted reasoning state: %+v", response.ToolCalls[0])
	}
	if response.Usage.PromptTokens != 13 || response.Usage.CacheReadTokens != 5 || response.Usage.CacheWriteTokens != 2 || response.Usage.ReasoningTokens != 3 {
		t.Fatalf("unexpected usage: %+v", response.Usage)
	}

	if captured["model"] != "codex-override" || captured["stream"] != true || captured["store"] != false || captured["custom"] != "value" {
		t.Fatalf("unexpected request controls: %#v", captured)
	}
	include, _ := captured["include"].([]any)
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %#v", captured["include"])
	}
	choice, _ := captured["tool_choice"].(map[string]any)
	if choice["type"] != "function" || choice["name"] != "lookup" || captured["parallel_tool_calls"] != false {
		t.Fatalf("unexpected tool controls: choice=%#v parallel=%#v", choice, captured["parallel_tool_calls"])
	}
	reasoning, _ := captured["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" || reasoning["summary"] != "auto" {
		t.Fatalf("reasoning = %#v", reasoning)
	}
	text, _ := captured["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_schema" || format["name"] != "result" || format["strict"] != true {
		t.Fatalf("text.format = %#v", format)
	}
	input, _ := captured["input"].([]any)
	if len(input) != 4 {
		t.Fatalf("input length = %d, input=%#v", len(input), input)
	}
	if input[0].(map[string]any)["role"] != "developer" || input[2].(map[string]any)["type"] != "function_call" || input[3].(map[string]any)["type"] != "function_call_output" {
		t.Fatalf("unexpected converted input: %#v", input)
	}
}

func TestBuildCodexRequestReplaysEncryptedReasoning(t *testing.T) {
	signature := `{"type":"reasoning","id":"reasoning-1","summary":[],"encrypted_content":"opaque-state"}`
	request := buildCodexRequest("codex", []service.Message{{
		Role: "assistant",
		Content: []service.ContentBlock{{
			Type:             "tool_use",
			ID:               "call-1",
			Name:             "lookup",
			Input:            map[string]any{"query": "go"},
			ThoughtSignature: signature,
		}},
	}}, nil, nil)
	input := request["input"].([]any)
	if len(input) != 2 || input[0].(map[string]any)["type"] != "reasoning" || input[1].(map[string]any)["type"] != "function_call" {
		t.Fatalf("encrypted reasoning state was not replayed before tool call: %#v", input)
	}
}

func TestBuildCodexRequestOmitsUnsupportedControls(t *testing.T) {
	maxTokens := 4096
	maxCompletionTokens := 8192
	temperature := 0.5
	topP := 0.9
	request := buildCodexRequest("gpt-5.5", nil, nil, &service.ChatOptions{
		MaxTokens:           &maxTokens,
		MaxCompletionTokens: &maxCompletionTokens,
		Temperature:         &temperature,
		TopP:                &topP,
		Metadata:            map[string]any{"key": "value"},
	})

	for _, key := range []string{"max_output_tokens", "temperature", "top_p", "metadata"} {
		if _, ok := request[key]; ok {
			t.Errorf("unsupported Codex control %q was included: %#v", key, request[key])
		}
	}
}

func TestBuildCodexRequestAllowsExplicitExtraBodyControls(t *testing.T) {
	request := buildCodexRequest("gpt-5.5", nil, nil, &service.ChatOptions{
		ExtraBody: map[string]any{"max_output_tokens": 1024},
	})

	if request["max_output_tokens"] != 1024 {
		t.Fatalf("explicit max_output_tokens = %#v", request["max_output_tokens"])
	}
}

func TestCodexProviderReturnsTyped429(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer server.Close()

	provider := NewCodexProvider("codex", "account", NewCodexTokenSource("token", "", "account", time.Time{}, nil),
		WithCodexBaseURL(server.URL),
		WithCodexHTTPClient(server.Client()),
	)
	_, err := provider.Chat(context.Background(), "", nil, nil, nil)
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	var rateLimitErr *service.RateLimitError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("error type = %T, want *service.RateLimitError", err)
	}
	if rateLimitErr.StatusCode != http.StatusTooManyRequests || rateLimitErr.RetryAfter != 2*time.Second || rateLimitErr.Provider != "openai-codex" {
		t.Fatalf("unexpected rate-limit error: %+v", rateLimitErr)
	}
}

func TestCodexProviderRefreshesAndRetriesOnceOnUnauthorized(t *testing.T) {
	oldAccessToken := codexTestJWT(t, "account", time.Now().Add(time.Hour))
	newAccessToken := codexTestJWT(t, "account", time.Now().Add(2*time.Hour))
	var inferenceCalls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_ = json.NewEncoder(w).Encode(map[string]string{
				"access_token":  newAccessToken,
				"refresh_token": "refresh-new",
			})
			return
		}
		if inferenceCalls.Add(1) == 1 {
			if r.Header.Get("Authorization") != "Bearer "+oldAccessToken {
				t.Errorf("first Authorization = %q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+newAccessToken {
			t.Errorf("retry Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{}}\n\n")
	}))
	defer server.Close()

	source := NewCodexTokenSource(oldAccessToken, "refresh-old", "account", time.Now().Add(time.Hour), server.Client(), codexTestEndpoints(server.URL))
	provider := NewCodexProvider("codex", "account", source,
		WithCodexBaseURL(server.URL+"/responses"),
		WithCodexHTTPClient(server.Client()),
	)
	response, err := provider.Chat(context.Background(), "", nil, nil, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if response.FinishReason != "stop" || inferenceCalls.Load() != 2 {
		t.Fatalf("response=%+v inference_calls=%d", response, inferenceCalls.Load())
	}
}

func TestCodexProviderProxyUsesCodexRootAndAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/models" || r.URL.Query().Get("limit") != "10" {
			t.Errorf("proxied URL = %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("ChatGPT-Account-ID") != "account" {
			t.Errorf("missing proxy auth headers: %#v", r.Header)
		}
		_, _ = w.Write([]byte("proxied"))
	}))
	defer server.Close()

	provider := NewCodexProvider("codex", "account", NewCodexTokenSource("token", "", "account", time.Time{}, nil),
		WithCodexBaseURL(server.URL+"/backend-api/codex/responses"),
		WithCodexHTTPClient(server.Client()),
	)
	req := httptest.NewRequest(http.MethodGet, "/gateway?limit=10", strings.NewReader(""))
	recorder := httptest.NewRecorder()
	if err := provider.Proxy(recorder, req, "/models"); err != nil {
		t.Fatalf("Proxy: %v", err)
	}
	if recorder.Code != http.StatusOK || recorder.Body.String() != "proxied" {
		t.Fatalf("proxy response = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestNormalizeCodexClientVersion(t *testing.T) {
	if got := NormalizeCodexClientVersion("v1.2.3-beta.1"); got != "1.2.3" {
		t.Fatalf("NormalizeCodexClientVersion = %q", got)
	}
	if got := NormalizeCodexClientVersion("dev"); got != "0.0.0" {
		t.Fatalf("invalid version normalized to %q", got)
	}
}

func TestCodexProviderModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/backend-api/codex/models" || r.URL.Query().Get("client_version") != "1.2.3" {
			t.Errorf("models URL = %s", r.URL.String())
		}
		if r.Header.Get("Authorization") != "Bearer token" || r.Header.Get("ChatGPT-Account-ID") != "account" {
			t.Errorf("models auth headers = %#v", r.Header)
		}
		_, _ = w.Write([]byte(`{"models":[{"slug":"gpt-codex-a"},{"slug":"gpt-codex-b"}]}`))
	}))
	defer server.Close()

	provider := NewCodexProvider("codex", "account", NewCodexTokenSource("token", "", "account", time.Time{}, nil),
		WithCodexBaseURL(server.URL+"/backend-api/codex/responses"),
		WithCodexHTTPClient(server.Client()),
		WithCodexClientVersion("v1.2.3-beta.1"),
	)
	models, err := provider.Models(context.Background())
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) != 2 || models[0] != "gpt-codex-a" || models[1] != "gpt-codex-b" {
		t.Fatalf("models = %#v", models)
	}
}
