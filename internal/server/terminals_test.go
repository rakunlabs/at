package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/alan"

	"github.com/rakunlabs/at/internal/service"
	hostterminal "github.com/rakunlabs/at/internal/service/terminal"
)

type terminalFixtureStore struct {
	service.Storer
	service.TerminalStorer
	*fakeAuthStore
	items []service.TerminalSession
}

func (f *terminalFixtureStore) ListTerminals(_ context.Context, owner string) ([]service.TerminalSession, error) {
	items := []service.TerminalSession{}
	for _, item := range f.items {
		if item.OwnerID == owner {
			items = append(items, item)
		}
	}
	return items, nil
}
func (f *terminalFixtureStore) GetTerminal(_ context.Context, owner, id string) (*service.TerminalSession, error) {
	for _, item := range f.items {
		if item.OwnerID == owner && item.ID == id {
			return &item, nil
		}
	}
	return nil, nil
}
func (f *terminalFixtureStore) GetTerminalPreferences(context.Context, string) (service.TerminalPreferences, error) {
	return service.TerminalPreferences{DefaultUsers: map[string]string{}}, nil
}

func TestTerminalHTTPAdmission(t *testing.T) {
	auth, authStore, mux := nativeFixture(t)
	store := &terminalFixtureStore{fakeAuthStore: authStore}
	s := &Server{ctx: t.Context(), store: store, nativeAuth: auth}
	s.initTerminals()
	item := service.TerminalSession{ID: ulid.Make().String(), OwnerID: "admin", TargetID: s.terminals.host.ID, TargetName: "host", Username: "root", Title: "Build"}
	store.items = []service.TerminalSession{item, {ID: ulid.Make().String(), OwnerID: "another-admin", Title: "Private terminal"}}
	api := mux.Group("/at/api/v1/terminals")
	api.Use(auth.require(true))
	api.GET("", s.ListTerminalsAPI)
	api.PUT("/{id}", s.UpdateTerminalAPI)
	api.GET("/{id}/ws", s.TerminalWebSocketAPI)
	admin := nativeLoginCookie(t, mux, "admin")
	reader := nativeLoginCookie(t, mux, "reader")
	for _, tt := range []struct {
		name   string
		cookie *http.Cookie
		want   int
	}{{"anonymous", nil, 401}, {"member", reader, 403}, {"admin", admin, 200}} {
		t.Run(tt.name, func(t *testing.T) {
			w := nativeRequest(mux, "GET", "/at/api/v1/terminals", "", "", tt.cookie)
			if w.Code != tt.want {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if tt.want == 200 && (strings.Contains(w.Body.String(), "Private terminal") || strings.Contains(w.Body.String(), "owner_id")) {
				t.Fatalf("private metadata leaked: %s", w.Body)
			}
		})
	}
	for _, origin := range []string{"", "https://evil.example"} {
		w := nativeRequest(mux, "GET", "/at/api/v1/terminals/"+item.ID+"/ws", "", origin, admin)
		if w.Code != 403 {
			t.Fatalf("websocket accepted origin %q: %d %s", origin, w.Code, w.Body)
		}
	}
	w := nativeRequest(mux, "PUT", "/at/api/v1/terminals/"+store.items[1].ID, `{"title":"stolen","position":0}`, "https://at.example", admin)
	if w.Code != 404 {
		t.Fatalf("cross-owner mutation: %d %s", w.Code, w.Body)
	}
	w = nativeRequest(mux, "PUT", "/at/api/v1/terminals/"+item.ID, `{"title":"changed","position":0,"owner":"another-admin"}`, "https://at.example", admin)
	if w.Code != 400 {
		t.Fatalf("client supplied owner accepted: %d %s", w.Code, w.Body)
	}
}

func TestTerminalLiveAuthorization(t *testing.T) {
	f := &fakeAuthStore{users: map[string]service.AuthUser{"admin": {ID: "admin", Admin: true}, "member": {ID: "member"}}, sessions: map[string]service.AuthSession{
		"admin-session": {UserID: "admin", ExpiresAt: time.Now().Add(time.Hour)}, "member-session": {UserID: "member", ExpiresAt: time.Now().Add(time.Hour)},
	}}
	m := &terminalManager{auth: f}
	for _, req := range []terminalRPC{{}, {Owner: "admin", Session: "member-session"}, {Owner: "member", Session: "member-session"}, {Owner: "wrong", Session: "admin-session"}} {
		if err := m.authorized(t.Context(), req); err == nil {
			t.Fatalf("accepted %+v", req)
		}
	}
	req := terminalRPC{Owner: "admin", Session: "admin-session"}
	if err := m.authorized(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	_, _ = f.InvalidateAuthUser(t.Context(), "admin", false)
	if err := m.authorized(t.Context(), req); err == nil {
		t.Fatal("revoked session retained host access")
	}
}

func TestTerminalAlanRoutingAndStreaming(t *testing.T) {
	// Two real QUIC backends on distinct loopback addresses, using Alan's public
	// DNS discovery API. This exercises the actual wire protocol, not a fake RPC.
	listener, err := net.ListenPacket("udp4", "127.0.0.2:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.LocalAddr().(*net.UDPAddr).Port
	listener.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	f := &fakeAuthStore{users: map[string]service.AuthUser{"admin": {ID: "admin", Admin: true}}, sessions: map[string]service.AuthSession{"login": {UserID: "admin", ExpiresAt: time.Now().Add(time.Hour)}}}
	store := &terminalFixtureStore{fakeAuthStore: f}
	makeNode := func(bind, dns, id string) *terminalManager {
		a, err := alan.New(alan.Config{BindAddr: bind, DNSAddr: dns, Port: port, RefreshInterval: 100 * time.Millisecond, Security: alan.SecurityConfig{Enabled: true, Key: []byte("terminal-integration-test-cluster-key")}})
		if err != nil {
			t.Fatal(err)
		}
		m := &terminalManager{ctx: ctx, store: store, auth: f, host: hostterminal.Host{ID: id, Name: id}, transport: a, links: map[string]*terminalLink{}, outputs: map[string]terminalOutput{}}
		m.registerTransport()
		go func() { _ = a.Start(ctx) }()
		t.Cleanup(func() { _ = a.Stop() })
		select {
		case <-a.Ready():
		case <-ctx.Done():
			t.Fatal("Alan failed to start")
		}
		return m
	}
	a := makeNode("127.0.0.2", "127.0.0.3", "host-a")
	b := makeNode("127.0.0.3", "127.0.0.2", "host-b")
	for a.transport.PeerCount() == 0 || b.transport.PeerCount() == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("Alan discovery timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}
	req := terminalRPC{Owner: "admin", Session: "login", Op: "host"}
	hosts, peers := a.targets(ctx, req)
	if len(hosts) != 2 || peers["host-b"] == nil {
		t.Fatalf("discovery: %+v %+v", hosts, peers)
	}
	reply, err := a.call(ctx, peers["host-b"], req)
	if err != nil || reply.Host == nil || reply.Host.ID != "host-b" {
		t.Fatalf("wrong backend: %+v %v", reply, err)
	}
	if _, err := a.target(ctx, req, "missing-host"); err == nil {
		t.Fatal("missing host fell back to local host")
	}
	// Byte-exact streaming back to the requesting backend, including ANSI and
	// UTF-8 sequences, without the agent loop's tool-output truncation.
	link := ulid.Make().String()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	a.mu.Lock()
	a.outputs[link] = terminalOutput{peer: peers["host-b"].String(), writer: writer}
	a.mu.Unlock()
	payload := strings.Repeat("\x1b[32mterminal-çalışıyor\x1b[0m\r\n", 10000)
	sent := make(chan error, 1)
	go func() {
		_, err := b.transport.SendToStream(ctx, b.transport.Peers()[0], terminalOutputType, strings.NewReader(link+"\n"+payload))
		sent <- err
	}()
	read := make(chan string, 1)
	go func() { data, _ := io.ReadAll(reader); read <- string(data) }()
	select {
	case got := <-read:
		if got != payload {
			t.Fatalf("stream changed: got %d bytes want %d", len(got), len(payload))
		}
	case <-ctx.Done():
		t.Fatal("stream timed out")
	}
	if err := <-sent; err != nil {
		t.Fatal(err)
	}
	_ = f.DeleteAuthSession(ctx, "login")
	if _, err := a.call(ctx, peers["host-b"], req); err == nil {
		t.Fatal("remote backend accepted revoked session")
	}
}
