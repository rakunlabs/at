package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// BindRuntimeExecution is the identity/machine entrypoint seam. The caller must
// construct provenance from AccessPrincipal or an immutable stored initiator,
// never decode it from an HTTP body, workflow inputs or model tool arguments.
// validate must revalidate the session/membership and reload policy on EACH call.
func (s *Server) BindRuntimeExecution(ctx context.Context, provenance service.ExecutionProvenance, validate service.ExecutionRevalidator) (context.Context, error) {
	workspaceID := provenance.WorkspaceID
	if !filepath.IsLocal(workspaceID) || filepath.Base(workspaceID) != workspaceID || workspaceID == "." {
		return nil, service.ErrExecutionDenied
	}
	base := s.taskWorkspaceBase()
	rootPath := filepath.Join(base, "workspaces", workspaceID)
	bound, err := service.BindExecution(ctx, provenance, rootPath, validate)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base, 0700); err != nil {
		return nil, fmt.Errorf("create workspace base: %w", err)
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, fmt.Errorf("open workspace base: %w", err)
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Join("workspaces", workspaceID), 0700); err != nil {
		return nil, fmt.Errorf("create workspace root: %w", err)
	}
	workspace, err := root.OpenRoot(filepath.Join("workspaces", workspaceID))
	if err != nil {
		return nil, fmt.Errorf("open workspace root: %w", err)
	}
	defer workspace.Close()
	for _, dir := range []string{"tasks", "assets/avatars", "assets/voices", "assets/uploads", "assets/series"} {
		if err := workspace.MkdirAll(dir, 0700); err != nil {
			return nil, fmt.Errorf("create workspace asset directory: %w", err)
		}
	}
	return bound, nil
}

// RuntimeExecutionPolicyAPI is wired by the parent at
// GET/PUT /api/v1/workspaces/{id}/execution-policy after identity/runtime binding.
// A normal workspaceadmin cannot change this policy, including restricted mode.
func (s *Server) RuntimeExecutionPolicyAPI(w http.ResponseWriter, r *http.Request) {
	var admitted bool
	if r, admitted = s.runtimeRequest(w, r); !admitted {
		return
	}
	store, ok := s.store.(service.ExecutionStorer)
	if !ok {
		httpResponse(w, "execution policy store not configured", http.StatusServiceUnavailable)
		return
	}
	p, _, ok := service.ExecutionFromContext(r.Context())
	workspaceID := r.PathValue("workspace")
	if workspaceID == "" {
		workspaceID = r.PathValue("id")
	}
	if !ok || p.WorkspaceID != workspaceID {
		httpResponse(w, "execution policy access denied", http.StatusForbidden)
		return
	}
	if err := service.CheckExecution(r.Context(), service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
		httpResponse(w, "execution policy access denied", http.StatusForbidden)
		return
	}
	if r.Method == http.MethodGet {
		policy, err := store.GetExecutionPolicy(r.Context(), p.WorkspaceID)
		if err != nil {
			httpResponse(w, "cannot load execution policy", http.StatusInternalServerError)
			return
		}
		httpResponseJSON(w, map[string]any{"policy": policy, "isolated_worker_supported": false, "trusted_host_is_tenant_isolation": false}, http.StatusOK)
		return
	}
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		httpResponse(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var policy service.ExecutionPolicy
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		httpResponse(w, "invalid execution policy", http.StatusBadRequest)
		return
	}
	if policy.WorkspaceID != "" && policy.WorkspaceID != p.WorkspaceID {
		httpResponse(w, "workspace mismatch", http.StatusForbidden)
		return
	}
	policy.WorkspaceID = p.WorkspaceID
	// Enforce the platform-admin fence here as well as in the concrete store,
	// so alternative stores cannot accidentally expose a weaker policy API.
	policy, err := service.PrepareExecutionPolicyChange(r.Context(), policy)
	if err != nil {
		runtimePolicyError(w, err)
		return
	}
	if err := store.SaveExecutionPolicy(r.Context(), policy); err != nil {
		runtimePolicyError(w, err)
		return
	}
	policy.Version++
	httpResponseJSON(w, policy, http.StatusOK)
}

func runtimePolicyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrIsolatedWorkerUnsupported):
		httpResponse(w, err.Error(), http.StatusNotImplemented)
	case errors.Is(err, service.ErrExecutionDenied):
		httpResponse(w, "execution policy access denied or version changed", http.StatusForbidden)
	default:
		httpResponse(w, "invalid execution policy", http.StatusBadRequest)
	}
}

// PersistRuntimeSubject stores the original actor for a task/trigger/bot binding.
// Existing records are immutable; updating configuration cannot replace an
// initiator with a more privileged workspace member. A fresh subject is needed.
func (s *Server) PersistRuntimeSubject(ctx context.Context, kind, id string) error {
	if kind != "task" && kind != "trigger" && kind != "bot" {
		return service.ErrExecutionDenied
	}
	if id == "" {
		return service.ErrExecutionDenied
	}
	store, ok := s.store.(service.ExecutionStorer)
	if !ok {
		return fmt.Errorf("execution provenance store not configured: %w", service.ErrExecutionDenied)
	}
	bound, err := service.DeriveExecution(ctx, kind+"/"+id, kind)
	if err != nil {
		return err
	}
	provenance, _, _ := service.ExecutionFromContext(bound)
	existing, err := store.GetExecutionProvenance(bound, provenance.RunID)
	if err != nil {
		return err
	}
	if existing != nil {
		if *existing != provenance {
			return service.ErrExecutionDenied
		}
		return nil
	}
	return store.CreateExecutionProvenance(bound, provenance)
}

// ResumeRuntimeSubject is ONLY for authenticated machine dispatch or the
// scheduler. Never call it merely because a browser supplied a subject ID.
func (s *Server) ResumeRuntimeSubject(ctx context.Context, kind, id string, validate service.ExecutionRevalidator) (context.Context, error) {
	if kind != "task" && kind != "trigger" && kind != "bot" {
		return nil, service.ErrExecutionDenied
	}
	if (kind == "trigger" || kind == "bot") && validate == nil {
		if services, ok := s.store.(service.ExecutionServiceStorer); ok {
			binding, err := services.GetExecutionServiceBinding(ctx, kind, id)
			if err != nil {
				return nil, err
			}
			if binding != nil {
				if binding.Revoked {
					return nil, service.ErrExecutionDenied
				}
				p := service.ExecutionProvenance{RunID: ulid.Make().String(), UserID: binding.UserID, WorkspaceID: binding.WorkspaceID, Source: kind, MembershipVersion: binding.MembershipVersion, PolicyVersion: binding.PolicyVersion, ServiceID: binding.ID, ServiceVersion: binding.Version}
				ctx = service.WithAccessPrincipal(ctx, service.AccessPrincipal{UserID: p.UserID, WorkspaceID: p.WorkspaceID, MembershipVersion: p.MembershipVersion})
				return s.BindRuntimeExecution(ctx, p, s.revalidateRuntimeExecution)
			}
		}
	}
	store, ok := s.store.(service.ExecutionStorer)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	provenance, err := store.GetExecutionProvenance(ctx, kind+"/"+id)
	if err != nil {
		return nil, err
	}
	if provenance == nil {
		return nil, service.ErrExecutionDenied
	}
	ctx = service.WithAccessPrincipal(ctx, service.AccessPrincipal{UserID: provenance.UserID, WorkspaceID: provenance.WorkspaceID, SessionID: provenance.SessionID, MembershipVersion: provenance.MembershipVersion})
	if validate == nil {
		validate = s.revalidateRuntimeExecution
	}
	return s.BindRuntimeExecution(ctx, *provenance, validate)
}

func (s *Server) persistRuntimeRun(ctx context.Context) error {
	p, _, ok := service.ExecutionFromContext(ctx)
	if !ok {
		return service.ErrExecutionDenied
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "execution.run"}); err != nil {
		return err
	}
	store, ok := s.store.(service.ExecutionStorer)
	if !ok {
		return fmt.Errorf("execution provenance store not configured: %w", service.ErrExecutionDenied)
	}
	existing, err := store.GetExecutionProvenance(ctx, p.RunID)
	if err != nil {
		return err
	}
	if existing != nil {
		if *existing != p {
			return service.ErrExecutionDenied
		}
		return nil
	}
	return store.CreateExecutionProvenance(ctx, p)
}

func (s *Server) RuntimeTriggerBindingAPI(w http.ResponseWriter, r *http.Request) {
	var ok bool
	if r, ok = s.runtimeRequest(w, r); !ok {
		return
	}
	if r.Method == http.MethodDelete {
		s.revokeRuntimeBinding(w, r, "trigger")
		return
	}
	if err := service.CheckExecution(r.Context(), service.ExecutionAction{Kind: "resource", Name: "triggers.use", ResourceID: r.PathValue("id")}); err != nil {
		httpResponse(w, "trigger access denied", http.StatusForbidden)
		return
	}
	if s.triggerStore == nil {
		httpResponse(w, "trigger store unavailable", http.StatusServiceUnavailable)
		return
	}
	trigger, err := s.triggerStore.GetTrigger(r.Context(), r.PathValue("id"))
	if err != nil || trigger == nil {
		httpResponse(w, "trigger unavailable", http.StatusNotFound)
		return
	}
	if err := service.CheckExecution(r.Context(), service.ExecutionAction{Kind: "resource", Name: "workflows.run", ResourceID: trigger.WorkflowID}); err != nil {
		httpResponse(w, "workflow execution denied", http.StatusForbidden)
		return
	}
	runAs, decodeErr := runtimeBindingUser(w, r)
	if decodeErr != nil {
		httpResponse(w, "invalid execution binding", http.StatusBadRequest)
		return
	}
	binding, err := s.saveRuntimeBinding(r.Context(), "trigger", trigger.ID, r.Method == http.MethodDelete, runAs)
	if err != nil {
		httpResponse(w, "trigger execution binding denied", http.StatusForbidden)
		return
	}
	httpResponseJSON(w, binding, http.StatusOK)
}

// Pass this resolver to Scheduler.SetExecutionContext before Start.
func (s *Server) runtimeSchedulerContext(ctx context.Context, triggerID string) (context.Context, error) {
	ctx, err := s.ResumeRuntimeSubject(ctx, "trigger", triggerID, nil)
	if err != nil {
		return nil, err
	}
	if err := service.CheckExecution(ctx, service.ExecutionAction{Kind: "resource", Name: "triggers.use", ResourceID: triggerID}); err != nil {
		return nil, err
	}
	return ctx, nil
}

func (s *Server) RuntimeBotBindingAPI(w http.ResponseWriter, r *http.Request) {
	var ok bool
	if r, ok = s.runtimeRequest(w, r); !ok {
		return
	}
	if r.Method == http.MethodDelete {
		s.revokeRuntimeBinding(w, r, "bot")
		return
	}
	if err := service.CheckExecution(r.Context(), service.ExecutionAction{Kind: "resource", Name: "bots.use", ResourceID: r.PathValue("id")}); err != nil {
		httpResponse(w, "bot access denied", http.StatusForbidden)
		return
	}
	runAs, decodeErr := runtimeBindingUser(w, r)
	if decodeErr != nil {
		httpResponse(w, "invalid execution binding", http.StatusBadRequest)
		return
	}
	binding, err := s.saveRuntimeBinding(r.Context(), "bot", r.PathValue("id"), r.Method == http.MethodDelete, runAs)
	if err != nil {
		httpResponse(w, "bot execution binding denied", http.StatusForbidden)
		return
	}
	httpResponseJSON(w, binding, http.StatusOK)
}

func (s *Server) saveRuntimeBinding(ctx context.Context, kind, id string, revoked bool, runAs ...string) (*service.ExecutionServiceBinding, error) {
	store, ok := s.store.(service.ExecutionServiceStorer)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	old, err := store.GetExecutionServiceBinding(ctx, kind, id)
	if err != nil {
		return nil, err
	}
	b := service.ExecutionServiceBinding{Kind: kind, SubjectID: id, Revoked: revoked}
	if len(runAs) > 0 {
		b.UserID = runAs[0]
	}
	if old != nil {
		b.Version = old.Version
	}
	return store.SaveExecutionServiceBinding(ctx, b)
}

func runtimeBindingUser(w http.ResponseWriter, r *http.Request) (string, error) {
	var req struct {
		RunAsUserID string `json:"run_as_user_id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return req.RunAsUserID, nil
}

func (s *Server) revokeRuntimeBinding(w http.ResponseWriter, r *http.Request, kind string) {
	b, err := s.saveRuntimeBinding(r.Context(), kind, r.PathValue("id"), true)
	if err != nil {
		httpResponse(w, "execution binding revocation denied", http.StatusForbidden)
		return
	}
	httpResponseJSON(w, b, http.StatusOK)
}

func (s *Server) createRuntimeTask(ctx context.Context, task service.Task) (*service.Task, error) {
	creator, ok := s.store.(service.ExecutionTaskCreator)
	if !ok {
		return nil, service.ErrExecutionDenied
	}
	return creator.CreateExecutionTask(ctx, task)
}
