package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

type developerReplayStore struct {
	service.ProviderStorer
	service.DeveloperSpaceStorer
	session service.DeveloperSession
}

func (store *developerReplayStore) GetDeveloperSession(ctx context.Context, id string) (*service.DeveloperSession, error) {
	p, ok := service.AccessPrincipalFromContext(ctx)
	if !ok || id != store.session.ID || p.UserID != store.session.OwnerUserID || p.WorkspaceID != store.session.WorkspaceID {
		return nil, service.ErrDeveloperSpaceNotFound
	}
	copy := store.session
	return &copy, nil
}

func TestDeveloperStreamDetachDiscoveryReplayAndStop(t *testing.T) {
	ctx, shutdown := context.WithCancel(t.Context())
	defer shutdown()
	store := &developerReplayStore{session: service.DeveloperSession{ID: "session", OwnerUserID: "owner", WorkspaceID: "workspace", Status: service.DeveloperSessionRunning}}
	s := &Server{ctx: ctx, store: store}
	var calls atomic.Int32
	started, cancelled := make(chan struct{}), make(chan struct{})
	worker := func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		p, _ := service.AccessPrincipalFromContext(r.Context())
		if p.UserID != "owner" || p.WorkspaceID != "workspace" {
			t.Error("detachment lost the initiating identity")
		}
		stream, _ := newDeveloperStream(w)
		stream.send(map[string]any{"type": "delta", "content": "working"})
		close(started)
		<-r.Context().Done()
		close(cancelled)
		stream.send(map[string]any{"type": "done", "session": store.session})
	}
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := r.Header.Get("User")
		if user == "" {
			user = "owner"
		}
		workspace := r.Header.Get("Workspace")
		if workspace == "" {
			workspace = "workspace"
		}
		r = r.WithContext(service.WithAccessPrincipal(r.Context(), service.AccessPrincipal{UserID: user, WorkspaceID: workspace}))
		r.SetPathValue("id", "session")
		r.SetPathValue("stream", "developer-test-stream")
		switch r.URL.Path {
		case "/run":
			s.startDeveloperStream(w, r, worker)
		case "/active":
			s.GetDeveloperActiveStreamAPI(w, r)
		case "/chat":
			s.ChatStreamAPI(w, r)
		default:
			s.DeveloperStreamAPI(w, r)
		}
	}))
	defer host.Close()
	post := func() *http.Response {
		r, _ := http.NewRequest("POST", host.URL+"/run", strings.NewReader(`{}`))
		r.Header.Set("X-AT-Stream-ID", "developer-test-stream")
		resp, err := host.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := post()
	<-started
	first := make([]byte, len("data: {\"content\":\"working\",\"type\":\"delta\"}\n\n"))
	if _, err := io.ReadFull(resp.Body, first); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	duplicate := post()
	duplicate.Body.Close()
	if duplicate.StatusCode != http.StatusConflict {
		t.Fatal("duplicate run admitted")
	}
	active, err := host.Client().Get(host.URL + "/active")
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		StreamID string `json:"stream_id"`
	}
	if err := json.NewDecoder(active.Body).Decode(&state); err != nil {
		t.Fatal(err)
	}
	active.Body.Close()
	if state.StreamID != "developer-test-stream" {
		t.Fatalf("discovery = %q", state.StreamID)
	}
	select {
	case <-cancelled:
		t.Fatal("page disconnect stopped the agent")
	default:
	}
	for _, header := range []string{"User", "Workspace"} {
		r, _ := http.NewRequest("GET", host.URL+"/replay?offset=0", nil)
		r.Header.Set(header, "foreign")
		foreign, err := host.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		foreign.Body.Close()
		if foreign.StatusCode != http.StatusNotFound {
			t.Fatalf("foreign replay = %d", foreign.StatusCode)
		}
	}
	chat, err := host.Client().Get(host.URL + "/chat?offset=0")
	if err != nil {
		t.Fatal(err)
	}
	chat.Body.Close()
	if chat.StatusCode != http.StatusNotFound {
		t.Fatal("developer stream admitted through Sessions")
	}
	s.cancelDeveloperStreams(&store.session)
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not cancel execution")
	}
	replay, err := host.Client().Get(host.URL + "/replay?offset=0")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(replay.Body)
	replay.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "working") || !strings.Contains(string(data), `"type":"done"`) {
		t.Fatalf("replay = %s", data)
	}
	if calls.Load() != 1 {
		t.Fatalf("work ran %d times", calls.Load())
	}
}

func TestDeveloperStreamShutdownRetainsIdentity(t *testing.T) {
	ctx, shutdown := context.WithCancel(t.Context())
	s := &Server{ctx: ctx}
	r := httptest.NewRequest("POST", "/run", strings.NewReader(`{}`))
	r = r.WithContext(service.WithAccessPrincipal(r.Context(), service.AccessPrincipal{UserID: "owner", WorkspaceID: "workspace"}))
	r.Header.Set("X-AT-Stream-ID", "developer-shutdown-stream")
	finished := make(chan struct{})
	started := make(chan struct{})
	go func() {
		s.startReplayStream(httptest.NewRecorder(), r, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
			if !errors.Is(r.Context().Err(), context.Canceled) {
				t.Error("shutdown did not cancel")
			}
		}, true)
		close(finished)
	}()
	<-started
	shutdown()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown left run active")
	}
}
