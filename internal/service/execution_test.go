package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func executionTestContext(t *testing.T, root string, policy ExecutionPolicy, admin bool, revoked *atomic.Bool) context.Context {
	t.Helper()
	p := ExecutionProvenance{RunID: "run", UserID: "user", WorkspaceID: policy.WorkspaceID, Source: "test", MembershipVersion: 1, PolicyVersion: policy.Version}
	ctx, err := BindExecution(t.Context(), p, root, func(ctx context.Context, p ExecutionProvenance, a ExecutionAction) (ExecutionValidation, error) {
		return ExecutionValidation{Policy: policy, MembershipVersion: 1, PlatformAdmin: admin, Allowed: !revoked.Load() && a.ResourceID != "other-workspace-resource"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestExecutionPolicyAuthorityAndRevocation(t *testing.T) {
	for _, tt := range []struct {
		name, mode         string
		admin, hostAllowed bool
	}{
		{"restricted member", ExecutionRestricted, false, false},
		{"restricted platform admin", ExecutionRestricted, true, false},
		{"explicit trusted member", ExecutionTrustedHost, false, true},
		{"explicit trusted platform admin", ExecutionTrustedHost, true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := ExecutionPolicy{WorkspaceID: "w", Mode: tt.mode, GrantedBy: "platform-user", AllowedTools: []string{"bash_execute", "file_read", "future_tool"}}
			var revoked atomic.Bool
			ctx := executionTestContext(t, t.TempDir(), policy, tt.admin, &revoked)
			err := CheckExecution(ctx, ExecutionAction{Kind: "tool", Name: "bash_execute"})
			if (err == nil) != tt.hostAllowed {
				t.Fatalf("host=%v: %v", tt.hostAllowed, err)
			}
			if CheckExecution(ctx, ExecutionAction{Kind: "tool", Name: "future_tool"}) == nil {
				t.Fatal("unknown tool allowed")
			}
			if CheckExecution(ctx, ExecutionAction{Kind: "resource", Name: "agents.run", ResourceID: "other-workspace-resource"}) == nil {
				t.Fatal("cross-scope reference allowed")
			}
			if CheckExecution(ctx, ExecutionAction{Kind: "file", Name: "files.read", WorkspaceID: "other"}) == nil {
				t.Fatal("workspace override allowed")
			}
			_, err = PrepareExecutionPolicyChange(ctx, ExecutionPolicy{WorkspaceID: "w", Mode: ExecutionTrustedHost})
			if (err == nil) != tt.admin {
				t.Fatalf("policy edit admin=%v: %v", tt.admin, err)
			}
			revoked.Store(true)
			if !errors.Is(CheckExecution(ctx, ExecutionAction{Kind: "tool", Name: "file_read"}), ErrExecutionDenied) {
				t.Fatal("revoked actor retained authority")
			}
		})
	}
	if !errors.Is(CheckExecution(context.Background(), ExecutionAction{Kind: "tool", Name: "bash_execute"}), ErrExecutionDenied) {
		t.Fatal("blank context allowed")
	}
	if !errors.Is(ValidateExecutionMode(ExecutionIsolatedWorker), ErrIsolatedWorkerUnsupported) {
		t.Fatal("claimed unsupported worker")
	}
}

func TestExecutionConcurrentRootContainment(t *testing.T) {
	parent := t.TempDir()
	roots := []string{filepath.Join(parent, "a"), filepath.Join(parent, "b")}
	for i, root := range roots {
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "media"), []byte{byte('a' + i)}, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(roots[1], filepath.Join(roots[0], "escape")); err != nil {
		t.Fatal(err)
	}
	var revoked atomic.Bool
	ctxA := executionTestContext(t, roots[0], ExecutionPolicy{WorkspaceID: "a"}, false, &revoked)
	for _, path := range []string{"../b/media", roots[1] + "/media"} {
		r, _, err := OpenExecutionRoot(ctxA, path, false)
		if r != nil {
			r.Close()
		}
		if err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	r, name, err := OpenExecutionRoot(ctxA, "escape/media", false)
	if err != nil {
		t.Fatal(err)
	}
	if f, err := OpenExecutionFile(r, name, os.O_RDONLY, 0); err == nil {
		f.Close()
		t.Fatal("followed escaping symlink")
	}
	r.Close()
	var wg sync.WaitGroup
	for i, root := range roots {
		ctx := executionTestContext(t, root, ExecutionPolicy{WorkspaceID: string(rune('a' + i))}, false, &revoked)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				r, name, err := OpenExecutionRoot(ctx, "media", false)
				if err != nil {
					t.Error(err)
					return
				}
				f, err := OpenExecutionFile(r, name, os.O_RDONLY, 0)
				r.Close()
				if err != nil {
					t.Error(err)
					return
				}
				b := make([]byte, 1)
				_, err = f.Read(b)
				f.Close()
				if err != nil || b[0] != byte('a'+i) {
					t.Errorf("cross-root content %q: %v", b, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestExecutionVersionFence(t *testing.T) {
	for _, which := range []string{"membership", "policy"} {
		t.Run(which, func(t *testing.T) {
			var changed atomic.Bool
			ctx, err := BindExecution(t.Context(), ExecutionProvenance{RunID: "r", UserID: "u", WorkspaceID: "w", Source: "test", MembershipVersion: 1, PolicyVersion: 1}, t.TempDir(), func(context.Context, ExecutionProvenance, ExecutionAction) (ExecutionValidation, error) {
				v := ExecutionValidation{Allowed: true, MembershipVersion: 1, Policy: ExecutionPolicy{WorkspaceID: "w", Version: 1}}
				if changed.Load() {
					if which == "membership" {
						v.MembershipVersion++
					} else {
						v.Policy.Version++
					}
				}
				return v, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			changed.Store(true)
			if !errors.Is(CheckExecution(ctx, ExecutionAction{Kind: "resource", Name: "execution.run"}), ErrExecutionDenied) {
				t.Fatal("stale run remained authorized")
			}
		})
	}
}

func TestPlatformExecutionRequiresExplicitLiveAdmin(t *testing.T) {
	var admin atomic.Bool
	validate := func(context.Context, ExecutionProvenance, ExecutionAction) (ExecutionValidation, error) {
		return ExecutionValidation{Allowed: true, PlatformAdmin: admin.Load(), Policy: ExecutionPolicy{WorkspaceID: "w", Mode: ExecutionTrustedHost, GrantedBy: "platform"}}, nil
	}
	p := ExecutionProvenance{RunID: "r", UserID: "u", WorkspaceID: "w", Source: "explicit-boot"}
	root := t.TempDir()
	if _, err := BindPlatformExecution(t.Context(), p, root, validate); !errors.Is(err, ErrExecutionDenied) {
		t.Fatal("workspace member entered compatibility mode")
	}
	admin.Store(true)
	ctx, err := BindPlatformExecution(t.Context(), p, root, validate)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckExecution(ctx, ExecutionAction{Kind: "tool", Name: "bash_execute"}); err != nil {
		t.Fatal(err)
	}
	if err := CheckExecution(ctx, ExecutionAction{Kind: "tool", Name: "unregistered-unknown"}); !errors.Is(err, ErrExecutionDenied) {
		t.Fatal("compatibility authorized an unknown executor")
	}
	admin.Store(false)
	if err := CheckExecution(ctx, ExecutionAction{Kind: "tool", Name: "bash_execute"}); !errors.Is(err, ErrExecutionDenied) {
		t.Fatal("demoted admin retained authority")
	}
	if err := CheckExecution(context.Background(), ExecutionAction{Kind: "tool", Name: "bash_execute"}); !errors.Is(err, ErrExecutionDenied) {
		t.Fatal("blank context inherited boot authority")
	}
}
