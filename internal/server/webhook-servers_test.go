package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func githubSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// A dedicated webhook server opens its own port, serves only the webhooks
// bound to it, verifies signatures, and hides webhooks from the main route.
func TestWebhookServerEndToEnd(t *testing.T) {
	f := newMachineFixture(t)
	f.s.triggerStore, f.s.workflowStore = f.store, f.store
	var err error
	f.s.webhookListeners = newWebhookListenerManager(f.s)
	t.Cleanup(f.s.webhookListeners.Close)
	// Workflow nodes need the policy to admit them; rebinding picks up the
	// new policy version for the fixture's own writes.
	actor, _ := service.AccessPrincipalFromContext(f.ctx)
	human := service.WithAccessPrincipal(t.Context(), actor)
	policy := httptest.NewRequest(http.MethodPut, "/execution-policy", strings.NewReader(`{"mode":"trusted_host","version":1,"allow_all_tools":true,"allow_all_nodes":true}`)).WithContext(human)
	policy.SetPathValue("workspace", f.workspace)
	pw := httptest.NewRecorder()
	f.s.RuntimeExecutionPolicyAPI(pw, policy)
	if pw.Code != http.StatusOK {
		t.Fatalf("policy: %d %s", pw.Code, pw.Body.String())
	}
	if f.ctx, err = f.s.bindRuntimePrincipal(human, "test"); err != nil {
		t.Fatal(err)
	}

	var wf *service.Workflow
	graph := service.WorkflowGraph{Nodes: []service.WorkflowNode{{ID: "in", Type: "input"}, {ID: "out", Type: "output"}}, Edges: []service.WorkflowEdge{{Source: "in", Target: "out", SourceHandle: "output", TargetHandle: "input"}}}
	wf, err = f.store.CreateWorkflow(f.ctx, service.Workflow{Name: "hooked", Graph: graph})
	if err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	srv, err := f.store.CreateWebhookServer(f.ctx, service.WebhookServer{Name: "hooks", BindHost: "127.0.0.1", Port: port, Enabled: true, AllWorkspaces: true})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := f.store.CreateTrigger(f.ctx, service.Trigger{
		Type: "http", TargetID: wf.ID, Enabled: true, Public: true, Alias: "gh", HideFromMain: true,
		WebhookRoutes: []service.WebhookRoute{{ServerID: srv.ID, Path: "github/push"}},
		Signature:     &service.WebhookSignature{Scheme: "github", Secret: "topsecret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.saveRuntimeBinding(f.ctx, "trigger", hidden.ID, false, f.member); err != nil {
		t.Fatal(err)
	}
	mainOnly, err := f.store.CreateTrigger(f.ctx, service.Trigger{Type: "http", TargetID: wf.ID, Enabled: true, Public: true, Alias: "main-only"})
	if err != nil {
		t.Fatal(err)
	}

	f.s.webhookListeners.Reload(t.Context())
	if st := f.s.webhookListeners.Status(srv.ID); st.State != "running" {
		t.Fatalf("listener not running: %+v", st)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	do := func(method, path string, body []byte, header map[string]string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest(method, base+path, strings.NewReader(string(body)))
		for k, v := range header {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, _ := do("GET", "/healthz", nil, nil); code != 200 {
		t.Fatalf("health: %d", code)
	}
	// Nothing but webhooks lives on the dedicated port.
	for _, path := range []string{"/api/v1/info", "/gateway/v1/models", "/webhooks/gh", "/main-only", "/" + mainOnly.ID} {
		if code, _ := do("POST", path, nil, nil); code != 404 {
			t.Fatalf("%s reachable on webhook port: %d", path, code)
		}
	}
	body := []byte(`{"ref":"main"}`)
	if code, msg := do("POST", "/github/push", body, nil); code != 401 || !strings.Contains(msg, "signature") {
		t.Fatalf("unsigned accepted: %d %s", code, msg)
	}
	if code, _ := do("POST", "/github/push", body, map[string]string{"X-Hub-Signature-256": githubSignature("wrong", body)}); code != 401 {
		t.Fatalf("bad signature accepted: %d", code)
	}
	if code, _ := do("GET", "/github/push", nil, nil); code != 405 {
		t.Fatalf("GET not refused: %d", code)
	}
	code, msg := do("POST", "/github/push?sync=true", body, map[string]string{"X-Hub-Signature-256": githubSignature("topsecret", body)})
	if code != 200 || !strings.Contains(msg, `"completed"`) {
		t.Fatalf("signed webhook: %d %s", code, msg)
	}

	// The hidden trigger answers 404 on the main route; the other does not.
	main := func(id string) int {
		r := httptest.NewRequest(http.MethodPost, "/webhooks/"+id, strings.NewReader("{}"))
		r.SetPathValue("id", id)
		w := httptest.NewRecorder()
		f.s.WebhookAPI(w, r)
		return w.Code
	}
	if c := main("gh"); c != 404 {
		t.Fatalf("hidden trigger reachable on main route: %d", c)
	}
	if c := main(hidden.ID); c != 404 {
		t.Fatalf("hidden trigger reachable by ID on main route: %d", c)
	}
	if c := main("main-only"); c == 404 {
		t.Fatal("ordinary trigger lost its main route")
	}

	// Deliveries are recorded asynchronously.
	deadline := time.Now().Add(5 * time.Second)
	for {
		list, err := f.store.ListWebhookDeliveries(f.ctx, hidden.ID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(list) >= 3 {
			if list[0].Status != 200 || list[0].RunID == "" || list[0].ServerID != srv.ID {
				t.Fatalf("latest delivery: %+v", list[0])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("deliveries not recorded: %+v", list)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Address allowlist and disabling close the door.
	srv.AllowedCIDRs = []string{"10.0.0.0/8"}
	srv.TLSKey = ""
	if _, err := f.store.UpdateWebhookServer(f.ctx, srv.ID, *srv); err != nil {
		t.Fatal(err)
	}
	f.s.webhookListeners.Reload(t.Context())
	if code, _ := do("POST", "/github/push", body, nil); code != 403 {
		t.Fatalf("allowlist not enforced: %d", code)
	}
	srv.Enabled = false
	if _, err := f.store.UpdateWebhookServer(f.ctx, srv.ID, *srv); err != nil {
		t.Fatal(err)
	}
	f.s.webhookListeners.Reload(t.Context())
	if _, err := http.Get(base + "/healthz"); err == nil {
		t.Fatal("disabled server still listening")
	}

	// A busy port is a status, not a crash.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	srv.Enabled, srv.AllowedCIDRs, srv.Port = true, nil, ln.Addr().(*net.TCPAddr).Port
	if _, err := f.store.UpdateWebhookServer(f.ctx, srv.ID, *srv); err != nil {
		t.Fatal(err)
	}
	f.s.webhookListeners.Reload(t.Context())
	if st := f.s.webhookListeners.Status(srv.ID); st.State != "error" || st.Error == "" {
		t.Fatalf("busy port status: %+v", st)
	}
}

func base64HMAC(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookSignature(t *testing.T) {
	body := []byte(`{"a":1}`)
	now := time.Unix(1_700_000_000, 0)
	stripe := func(secret string, ts int64) string {
		mac := hmac.New(sha256.New, []byte(secret))
		fmt.Fprintf(mac, "%d.%s", ts, body)
		return fmt.Sprintf("t=%d,v1=deadbeef,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
	}
	tests := []struct {
		name   string
		sig    service.WebhookSignature
		header map[string]string
		ok     bool
	}{
		{"github ok", service.WebhookSignature{Scheme: "github", Secret: "k"}, map[string]string{"X-Hub-Signature-256": githubSignature("k", body)}, true},
		{"github uppercase hex", service.WebhookSignature{Scheme: "github", Secret: "k"}, map[string]string{"X-Hub-Signature-256": "sha256=" + strings.ToUpper(githubSignature("k", body)[7:])}, true},
		{"github missing prefix", service.WebhookSignature{Scheme: "github", Secret: "k"}, map[string]string{"X-Hub-Signature-256": githubSignature("k", body)[7:]}, false},
		{"github wrong secret", service.WebhookSignature{Scheme: "github", Secret: "k"}, map[string]string{"X-Hub-Signature-256": githubSignature("other", body)}, false},
		{"github missing", service.WebhookSignature{Scheme: "github", Secret: "k"}, nil, false},
		{"stripe ok", service.WebhookSignature{Scheme: "stripe", Secret: "k"}, map[string]string{"Stripe-Signature": stripe("k", now.Unix())}, true},
		{"stripe expired", service.WebhookSignature{Scheme: "stripe", Secret: "k"}, map[string]string{"Stripe-Signature": stripe("k", now.Add(-10*time.Minute).Unix())}, false},
		{"custom base64", service.WebhookSignature{Scheme: "hmac_sha256", Secret: "k", Header: "X-Sig", Encoding: "base64"}, map[string]string{"X-Sig": base64HMAC("k", body)}, true},
		{"custom base64 tampered", service.WebhookSignature{Scheme: "hmac_sha256", Secret: "k", Header: "X-Sig", Encoding: "base64"}, map[string]string{"X-Sig": base64HMAC("k", []byte("other"))}, false},
		{"no secret", service.WebhookSignature{Scheme: "hmac_sha256"}, map[string]string{"X-Signature": "x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.sig.Normalize()
			h := http.Header{}
			for k, v := range tt.header {
				h.Set(k, v)
			}
			err := verifyWebhookSignature(tt.sig, h, body, now)
			if (err == nil) != tt.ok {
				t.Fatalf("got %v want ok=%v", err, tt.ok)
			}
		})
	}
}
