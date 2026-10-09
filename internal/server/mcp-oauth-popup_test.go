package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPOAuthPopupResult(t *testing.T) {
	for _, ok := range []bool{true, false} {
		name := "success"
		if !ok {
			name = "failure"
		}
		t.Run(name, func(t *testing.T) {
			s := &Server{}
			w := httptest.NewRecorder()
			state := "workspace.nonce</script>"
			s.renderMCPOAuthResult(w, ok, "Account <connected>", "c1", state)
			wantStatus := http.StatusOK
			if !ok {
				wantStatus = http.StatusBadRequest
			}
			if w.Code != wantStatus || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("response: %d %v", w.Code, w.Header())
			}
			body := w.Body.String()
			_, rest, found := strings.Cut(body, `<script type="application/json" id="result">`)
			if !found {
				t.Fatal("missing result payload")
			}
			payload, _, _ := strings.Cut(rest, "</script>")
			var result struct {
				Type         string `json:"type"`
				OK           bool   `json:"ok"`
				State        string `json:"state"`
				ConnectionID string `json:"connection_id"`
			}
			if err := json.Unmarshal([]byte(payload), &result); err != nil {
				t.Fatal(err)
			}
			if result.Type != "at-mcp-oauth-result" || result.OK != ok || result.State != state || result.ConnectionID != "c1" {
				t.Fatalf("result: %+v", result)
			}
			if strings.Contains(body, state) || !strings.Contains(body, "Account &lt;connected&gt;") {
				t.Fatal("popup data is not HTML escaped")
			}
			for _, fragment := range []string{`new BroadcastChannel("at-mcp-oauth:"+r.state)`, "c.postMessage(r)", "window.opener.postMessage(r,o)", "window.close()"} {
				if !strings.Contains(body, fragment) {
					t.Fatalf("missing bridge fragment: %s", fragment)
				}
			}
			if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'nonce-") {
				t.Fatal("missing nonce policy")
			}
		})
	}
}

func TestMCPOAuthPopupRefusalPreservesState(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.MCPOAuthCallbackAPI(w, httptest.NewRequest(http.MethodGet, mcpOAuthCallbackPath+"?error=access_denied&state=workspace.nonce", nil))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"state":"workspace.nonce"`) {
		t.Fatalf("refusal lost ceremony state: %d %s", w.Code, w.Body.String())
	}
}
