package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

type traceExportStore struct {
	service.ProviderStorer
	mu       sync.Mutex
	settings map[string]service.TraceExportSettings
	saves    int
}

func (f *traceExportStore) LoadTraceExportSettings(_ context.Context, ws string) (service.TraceExportSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.settings[ws]; ok {
		return c, nil
	}
	return service.DefaultTraceExportSettings(), nil
}
func (f *traceExportStore) SaveTraceExportSettings(ctx context.Context, c service.TraceExportSettings) (service.TraceExportSettings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, _ := service.AccessPrincipalFromContext(ctx)
	if c.Version != f.settings[a.WorkspaceID].Version {
		return c, service.ErrTraceExportConflict
	}
	c.Version++
	f.settings[a.WorkspaceID] = c
	f.saves++
	return c, nil
}

func TestTraceExportSettingsAPI(t *testing.T) {
	var auth string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer receiver.Close()
	store := &traceExportStore{settings: map[string]service.TraceExportSettings{}}
	s := &Server{store: store}
	actor := service.AccessPrincipal{WorkspaceID: "one", UserID: "admin", Grants: service.WorkspaceRoleGrants("admin")}
	call := func(method string, c service.TraceExportSettings, a service.AccessPrincipal) *httptest.ResponseRecorder {
		b, _ := json.Marshal(c)
		r := httptest.NewRequest(method, "/api/v1/trace-export", strings.NewReader(string(b))).WithContext(service.WithAccessPrincipal(t.Context(), a))
		w := httptest.NewRecorder()
		s.TraceExportSettingsAPI(w, r)
		return w
	}
	c := service.DefaultTraceExportSettings()
	c.Endpoint = receiver.URL
	c.Headers["Authorization"] = "private-token"
	w := call("PUT", c, actor)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-token") {
		t.Fatalf("save/redaction: %d %s", w.Code, w.Body)
	}
	var saved service.TraceExportSettings
	_ = json.Unmarshal(w.Body.Bytes(), &saved)
	if w = call("POST", saved, actor); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok":true`) || auth != "private-token" {
		t.Fatalf("test: %d %s", w.Code, w.Body)
	}
	if store.saves != 1 {
		t.Fatal("test changed saved config")
	}
	if w = call("PUT", saved, actor); w.Code != 200 {
		t.Fatalf("preserve: %s", w.Body)
	}
	if store.settings["one"].Headers["Authorization"] != "private-token" {
		t.Fatal("masked secret overwritten")
	}
	if w = call("PUT", saved, actor); w.Code != 409 {
		t.Fatal("stale save accepted")
	}
	other := actor
	other.WorkspaceID = "two"
	if w = call("GET", c, other); w.Code != 200 || strings.Contains(w.Body.String(), receiver.URL) {
		t.Fatal("workspace settings leaked")
	}
	member := actor
	member.Grants = service.WorkspaceRoleGrants("member")
	for _, method := range []string{"GET", "PUT", "POST"} {
		if w = call(method, c, member); w.Code != 403 {
			t.Fatalf("member admitted: %s", method)
		}
	}
}

func TestTraceExportDeliveryWorkspaceAndContent(t *testing.T) {
	got := make(chan string, 4)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- r.Header.Get("Authorization") + string(b)
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer receiver.Close()
	a := service.DefaultTraceExportSettings()
	a.Enabled = true
	a.Endpoint = receiver.URL
	a.Headers["Authorization"] = "workspace-one"
	a.IncludeContent = true
	b := a
	b.Headers = map[string]string{"Authorization": "workspace-two"}
	b.IncludeContent = false
	store := &traceExportStore{settings: map[string]service.TraceExportSettings{"one": a, "two": b}}
	s := &Server{store: store}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	s.startTraceExport(ctx)
	s.enqueueTraceExport(service.LLMCall{WorkspaceID: "one", ID: "a", TraceID: "a"}, true, []byte("private-one"), nil)
	s.enqueueTraceExport(service.LLMCall{WorkspaceID: "two", ID: "b", TraceID: "b", Input: "private-tool"}, true, []byte("private-two"), nil)
	for range 2 {
		select {
		case wire := <-got:
			if strings.HasPrefix(wire, "workspace-one") {
				if !strings.Contains(wire, "private-one") || strings.Contains(wire, "private-two") {
					t.Fatal("workspace one payload mismatch")
				}
			} else if strings.Contains(wire, "private-") {
				t.Fatal("content leaked")
			}
		case <-time.After(3 * time.Second):
			t.Fatal("delivery did not run")
		}
	}
	// The full-body capture feature gates all content before it enters the queue.
	s.enqueueTraceExport(service.LLMCall{WorkspaceID: "one", ID: "c", TraceID: "c", Input: "private-tool", ErrorMessage: "private-error"}, false, []byte("private-body"), nil)
	select {
	case wire := <-got:
		if strings.Contains(wire, "private-") {
			t.Fatal("audit toggle bypassed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("delivery did not run")
	}
	store.mu.Lock()
	a.Enabled = false
	store.settings["one"] = a
	store.mu.Unlock()
	s.enqueueTraceExport(service.LLMCall{WorkspaceID: "one", ID: "d", TraceID: "d"}, false, nil, nil)
	select {
	case <-got:
		t.Fatal("disabled destination used stale settings")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTraceExportWorkspaceIdentity(t *testing.T) {
	ctx := service.WithAccessPrincipal(t.Context(), service.AccessPrincipal{WorkspaceID: "browser"})
	if got := traceExportWorkspace(ctx, &authResult{token: &service.APIToken{WorkspaceID: "token"}}); got != "token" {
		t.Fatal(got)
	}
	if got := traceExportWorkspace(ctx, nil); got != "browser" {
		t.Fatal(got)
	}
}
