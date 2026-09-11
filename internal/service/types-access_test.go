package service

import (
	"context"
	"testing"
)

func TestWorkspaceAccessBoundaries(t *testing.T) {
	p := AccessPrincipal{UserID: "u", WorkspaceID: "a", Role: "admin", Grants: WorkspaceRoleGrants("admin")}
	for _, tt := range []struct {
		name, cap string
		r         AccessResource
		want      bool
	}{
		{"read", "agents.read", AccessResource{WorkspaceID: "a"}, true},
		{"foreign", "agents.read", AccessResource{WorkspaceID: "b"}, false},
		{"blank", "agents.read", AccessResource{}, false},
		{"unknown", "agents.nuclear", AccessResource{WorkspaceID: "a"}, false},
		{"platform", "platform.execute", AccessResource{WorkspaceID: "a"}, false},
		{"retired kind", "privatechat.read", AccessResource{WorkspaceID: "a", OwnerID: "u"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.Allows(tt.cap, tt.r); got != tt.want {
				t.Fatalf("Allows=%v want %v", got, tt.want)
			}
		})
	}
	p.Denied = []string{"agents.read"}
	if p.Allows("agents.read", AccessResource{WorkspaceID: "a"}) {
		t.Fatal("admin bypassed deny")
	}
	p.PlatformAdmin = true
	if p.Allows("privatechat.read", AccessResource{WorkspaceID: "a"}) || p.Allows("agents.read", AccessResource{WorkspaceID: "b"}) {
		t.Fatal("platform bypassed the capability registry or the workspace boundary")
	}
	if AuthorizeAccess(context.Background(), "agents.read", AccessResource{WorkspaceID: "a"}) == nil {
		t.Fatal("absent principal allowed")
	}
}
func TestWorkspaceAccessSelectorsAndDelegation(t *testing.T) {
	g := AccessGrant{Capability: "files.read", ResourceIDs: []string{"one"}, PathPatterns: []string{"media/*.png"}}
	p := AccessPrincipal{WorkspaceID: "a", Grants: []AccessGrant{g}}
	for _, tt := range []struct {
		id, path string
		want     bool
	}{{"one", "media/a.png", true}, {"two", "media/a.png", false}, {"one", "media/a.mp4", false}, {"one", "media/../a.png", false}, {"one", "media/sub/a.png", false}, {"one", "", false}} {
		if got := p.Allows("files.read", AccessResource{WorkspaceID: "a", ID: tt.id, Path: tt.path}); got != tt.want {
			t.Fatalf("%+v got %v", tt, got)
		}
	}
	if CanDelegateAccess(p, AccessGrant{Capability: "files.read"}) {
		t.Fatal("delegation broadened selector")
	}
	if !CanDelegateAccess(p, g) {
		t.Fatal("same selector rejected")
	}
	for _, patterns := range [][]string{{}, {""}, {"["}, {"../*"}, {"/root/*"}} {
		if ValidateAccessGrant(AccessGrant{Capability: "files.read", PathPatterns: patterns}) == nil {
			t.Fatalf("accepted invalid %q", patterns)
		}
	}
	b := PermissionBundle{Keys: []string{"files.read"}, KeyPatterns: map[string][]string{"files.read": nil}}
	if _, err := b.Grants(); err == nil {
		t.Fatal("null pattern became unrestricted")
	}
	p.Grants = append(p.Grants, AccessGrant{Capability: "files.read"})
	if !p.Allows("files.read", AccessResource{WorkspaceID: "a", ID: "two", Path: "x"}) {
		t.Fatal("unrestricted union failed")
	}
	ctx := WithAccessPrincipal(context.Background(), p)
	p.Grants[0].PathPatterns[0] = "*"
	got, _ := AccessPrincipalFromContext(ctx)
	got.Grants[0].PathPatterns[0] = "*"
	again, _ := AccessPrincipalFromContext(ctx)
	if again.Grants[0].PathPatterns[0] != "media/*.png" {
		t.Fatal("context principal mutated")
	}
}
