package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

func TestGatewayMCPHandler_PublicAllowsMissingToken(t *testing.T) {
	store := newFakeMCPServerStore()
	store.servers["public"] = &service.MCPServer{ID: "public", Name: "public", Public: true}
	s := &Server{mcpServerStore: store}

	rr := httptest.NewRecorder()
	s.GatewayMCPHandler(rr, newMCPInitRequest("/gateway/v1/mcp/public", "public"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestGatewayMCPHandler_PrivateRequiresToken(t *testing.T) {
	store := newFakeMCPServerStore()
	store.servers["private"] = &service.MCPServer{ID: "private", Name: "private"}
	s := &Server{mcpServerStore: store}

	rr := httptest.NewRecorder()
	s.GatewayMCPHandler(rr, newMCPInitRequest("/gateway/v1/mcp/private", "private"))

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
}

func TestSkillServerMCPHandler_PublicAllowsMissingToken(t *testing.T) {
	store := newFakeSkillServerStore()
	store.servers["public"] = &service.SkillServer{ID: "public", Name: "public", Public: true}
	s := &Server{skillServerStore: store}

	rr := httptest.NewRecorder()
	s.SkillServerMCPHandler(rr, newMCPInitRequest("/gateway/v1/skill-servers/public", "public"))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusOK, rr.Body.String())
	}
}

func TestSkillServerMCPHandler_PrivateRequiresToken(t *testing.T) {
	store := newFakeSkillServerStore()
	store.servers["private"] = &service.SkillServer{ID: "private", Name: "private"}
	s := &Server{skillServerStore: store}

	rr := httptest.NewRecorder()
	s.SkillServerMCPHandler(rr, newMCPInitRequest("/gateway/v1/skill-servers/private", "private"))

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body: %s", rr.Code, http.StatusUnauthorized, rr.Body.String())
	}
}

func newMCPInitRequest(path, name string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
	req.SetPathValue("name", name)
	return req
}

type fakeSkillServerStore struct {
	servers map[string]*service.SkillServer
}

func newFakeSkillServerStore() *fakeSkillServerStore {
	return &fakeSkillServerStore{servers: map[string]*service.SkillServer{}}
}

func (f *fakeSkillServerStore) ListSkillServers(_ context.Context, _ *query.Query) (*service.ListResult[service.SkillServer], error) {
	out := make([]service.SkillServer, 0, len(f.servers))
	for _, srv := range f.servers {
		out = append(out, *srv)
	}
	return &service.ListResult[service.SkillServer]{Data: out}, nil
}

func (f *fakeSkillServerStore) GetSkillServer(_ context.Context, id string) (*service.SkillServer, error) {
	return f.servers[id], nil
}

func (f *fakeSkillServerStore) GetSkillServerByName(_ context.Context, name string) (*service.SkillServer, error) {
	for _, srv := range f.servers {
		if srv.Name == name {
			return srv, nil
		}
	}
	return nil, nil
}

func (f *fakeSkillServerStore) CreateSkillServer(_ context.Context, srv service.SkillServer) (*service.SkillServer, error) {
	if srv.ID == "" {
		srv.ID = srv.Name
	}
	f.servers[srv.ID] = &srv
	return &srv, nil
}

func (f *fakeSkillServerStore) UpdateSkillServer(_ context.Context, id string, srv service.SkillServer) (*service.SkillServer, error) {
	if _, ok := f.servers[id]; !ok {
		return nil, nil
	}
	srv.ID = id
	f.servers[id] = &srv
	return &srv, nil
}

func (f *fakeSkillServerStore) DeleteSkillServer(_ context.Context, id string) error {
	delete(f.servers, id)
	return nil
}
