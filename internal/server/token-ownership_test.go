package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestAPITokenCreationOwnership(t *testing.T) {
	f := newMachineFixture(t)
	actor, _ := service.AccessPrincipalFromContext(f.ctx)
	for _, tc := range []struct {
		name, body, owner string
		status            int
	}{
		{"legacy", `{"name":"legacy"}`, "", http.StatusCreated},
		{"workspace", `{"name":"shared","scope":"workspace","owner_user_id":"forged"}`, "", http.StatusCreated},
		{"personal", `{"name":"mine","scope":"personal","owner_user_id":"forged"}`, actor.UserID, http.StatusCreated},
		{"invalid", `{"name":"bad","scope":"global"}`, "", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", strings.NewReader(tc.body)).WithContext(f.ctx)
			w := httptest.NewRecorder()
			f.s.CreateAPITokenAPI(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if tc.status != http.StatusCreated {
				return
			}
			var created createTokenResponse
			if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
				t.Fatal(err)
			}
			if created.Info.OwnerUserID != tc.owner || created.Info.WorkspaceID != actor.WorkspaceID {
				t.Fatalf("incorrect ownership: %+v", created.Info)
			}
		})
	}
}
