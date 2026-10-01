package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func TestChatStreamDisconnectReplayAndOwnership(t *testing.T) {
	ctx, shutdown := context.WithCancel(t.Context())
	defer shutdown()
	s := &Server{ctx: ctx}
	var calls atomic.Int32
	release, finished := make(chan struct{}), make(chan struct{})
	worker := func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"text\":\"merhaba\"}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			t.Error("disconnect cancelled execution")
		}
		io.WriteString(w, "data: {\"text\":\" dünya\"}\n\ndata: [DONE]\n\n")
		close(finished)
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
		if r.Method == "POST" {
			s.startChatStream(w, r, worker)
		} else {
			r.SetPathValue("stream", "stream-test-identifier")
			s.ChatStreamAPI(w, r)
		}
	}))
	defer host.Close()
	post := func() *http.Response {
		r, _ := http.NewRequest("POST", host.URL, strings.NewReader(`{}`))
		r.Header.Set("X-AT-Stream-ID", "stream-test-identifier")
		response, err := host.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := post()
	first := make([]byte, len("data: {\"text\":\"merhaba\"}\n\n"))
	if _, err := io.ReadFull(response.Body, first); err != nil {
		t.Fatal(err)
	}
	response.Body.Close() // the producer must continue, not run again
	duplicate := post()
	io.Copy(io.Discard, duplicate.Body)
	duplicate.Body.Close()
	if duplicate.StatusCode != 409 {
		t.Fatal(duplicate.StatusCode)
	}
	for _, headers := range []map[string]string{{"User": "foreign"}, {"Workspace": "foreign"}} {
		r, _ := http.NewRequest("GET", host.URL+"?offset=0", nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		got, err := host.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		got.Body.Close()
		if got.StatusCode != 404 {
			t.Fatalf("foreign replay admitted: %d", got.StatusCode)
		}
	}
	close(release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("execution did not finish")
	}
	// Resume from the middle of a UTF-8 codepoint; replay is byte exact.
	all, err := host.Client().Get(host.URL + "?offset=0")
	if err != nil {
		t.Fatal(err)
	}
	full, err := io.ReadAll(all.Body)
	all.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	offset := strings.Index(string(full), "ü") + 1
	partial, err := host.Client().Get(host.URL + "?offset=" + strconv.Itoa(offset))
	if err != nil {
		t.Fatal(err)
	}
	rest, err := io.ReadAll(partial.Body)
	partial.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if string(rest) != string(full[offset:]) || !strings.HasSuffix(string(full), ": at-stream-complete\n\n") {
		t.Fatalf("replay mismatch: %q %q", full, rest)
	}
	if calls.Load() != 1 {
		t.Fatalf("execution repeated %d times", calls.Load())
	}
}

func TestChatStreamExplicitCancellationAndMissingReplay(t *testing.T) {
	s := &Server{ctx: t.Context()}
	request := func(method string) *http.Request {
		r := httptest.NewRequest(method, "/?offset=0", strings.NewReader(`{}`))
		r.SetPathValue("stream", "cancel-test-identifier")
		return r.WithContext(service.WithAccessPrincipal(r.Context(), service.AccessPrincipal{UserID: "owner", WorkspaceID: "workspace"}))
	}
	missing := httptest.NewRecorder()
	s.ChatStreamAPI(missing, request("GET"))
	if missing.Code != 404 {
		t.Fatal(missing.Code)
	}
	started, cancelled, attached := make(chan struct{}), make(chan struct{}), make(chan struct{})
	r := request("POST")
	r.Header.Set("X-AT-Stream-ID", "cancel-test-identifier")
	go func() {
		s.startChatStream(httptest.NewRecorder(), r, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
			close(cancelled)
		})
		close(attached)
	}()
	<-started
	w := httptest.NewRecorder()
	s.ChatStreamAPI(w, request("DELETE"))
	if w.Code != 204 {
		t.Fatal(w.Code)
	}
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("explicit stop didn't cancel")
	}
	<-attached
	w = httptest.NewRecorder()
	invalid := request("GET")
	invalid.URL.RawQuery = "offset=999999"
	s.ChatStreamAPI(w, invalid)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
}
