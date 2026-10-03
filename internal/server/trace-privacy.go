package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/at/internal/service"
)

// tracePrivacyCacheTTL bounds how stale another replica's view of the rules
// may be. Writes on this replica invalidate immediately.
const tracePrivacyCacheTTL = 10 * time.Second

// tracePrivacySuppressTTL is how long a trace stays suppressed after one of
// its observations matched. Agent runs and their tool calls finish well
// within it; the window renews on every further observation of the trace.
const tracePrivacySuppressTTL = 30 * time.Minute

// tracePrivacySuppressMax bounds the suppressed-trace map. Past it, the
// oldest entries are evicted; a lost entry costs at most the retroactive
// cleanup the next matching observation performs anyway.
const tracePrivacySuppressMax = 20000

// tracePrivacyRuntime holds the cached policy and the traces currently
// suppressed on this replica.
type tracePrivacyRuntime struct {
	mu       sync.Mutex
	policy   *service.TracePrivacyPolicy
	loadedAt time.Time

	suppressMu sync.Mutex
	suppressed map[string]tracePrivacySuppression
}

type tracePrivacySuppression struct {
	action string
	until  time.Time
}

func (s *Server) tracePrivacyStore() (service.TracePrivacyStorer, bool) {
	store, ok := s.store.(service.TracePrivacyStorer)
	return store, ok
}

// tracePrivacyPolicy returns the cached policy, refreshing it after the TTL.
// A failed refresh keeps the last known policy; with none loaded yet it
// reports no policy, so an unreachable store never blocks recording.
func (s *Server) tracePrivacyPolicy(ctx context.Context) *service.TracePrivacyPolicy {
	store, ok := s.tracePrivacyStore()
	if !ok {
		return nil
	}
	rt := &s.tracePrivacy
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.policy != nil && time.Since(rt.loadedAt) < tracePrivacyCacheTTL {
		return rt.policy
	}
	loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	policy, err := store.LoadTracePrivacyPolicy(loadCtx)
	if err != nil {
		slog.Warn("trace privacy policy unavailable; using last known rules", "error", err.Error())
		rt.loadedAt = time.Now()
		return rt.policy
	}
	rt.policy, rt.loadedAt = &policy, time.Now()
	return rt.policy
}

func (s *Server) invalidateTracePrivacy() {
	s.tracePrivacy.mu.Lock()
	s.tracePrivacy.loadedAt = time.Time{}
	s.tracePrivacy.mu.Unlock()
}

func traceSuppressionKey(workspaceID, traceID string) string {
	return workspaceID + "\x00" + traceID
}

// traceSuppression returns the action recorded for a trace, if any.
func (s *Server) traceSuppression(workspaceID, traceID string) string {
	rt := &s.tracePrivacy
	rt.suppressMu.Lock()
	defer rt.suppressMu.Unlock()
	e, ok := rt.suppressed[traceSuppressionKey(workspaceID, traceID)]
	if !ok {
		return service.TracePrivacyNone
	}
	if time.Now().After(e.until) {
		delete(rt.suppressed, traceSuppressionKey(workspaceID, traceID))
		return service.TracePrivacyNone
	}
	return e.action
}

// suppressTrace records an action for a trace and reports whether it is
// stricter than what was recorded before, i.e. whether observations already
// written must be cleaned up.
func (s *Server) suppressTrace(workspaceID, traceID, action string) bool {
	rt := &s.tracePrivacy
	rt.suppressMu.Lock()
	defer rt.suppressMu.Unlock()
	if rt.suppressed == nil {
		rt.suppressed = map[string]tracePrivacySuppression{}
	}
	key := traceSuppressionKey(workspaceID, traceID)
	now := time.Now()
	prev, had := rt.suppressed[key]
	if had && now.After(prev.until) {
		had = false
	}
	next := action
	if had {
		next = service.StricterTracePrivacy(prev.action, action)
	}
	if len(rt.suppressed) >= tracePrivacySuppressMax && !had {
		var oldestKey string
		var oldest time.Time
		for k, e := range rt.suppressed {
			if now.After(e.until) {
				delete(rt.suppressed, k)
				continue
			}
			if oldestKey == "" || e.until.Before(oldest) {
				oldestKey, oldest = k, e.until
			}
		}
		if len(rt.suppressed) >= tracePrivacySuppressMax && oldestKey != "" {
			delete(rt.suppressed, oldestKey)
		}
	}
	rt.suppressed[key] = tracePrivacySuppression{action: next, until: now.Add(tracePrivacySuppressTTL)}
	return !had || next != prev.action
}

// tracePrivacyDecision decides what happens to one observation. A trace is
// the unit of privacy: once any of its observations matches, every
// observation of that trace on this replica gets at least the same action,
// and observations already written are cleaned up. Without that, skipping a
// generation would leave its tool calls and run span behind, holding the same
// conversation content.
func (s *Server) tracePrivacyDecision(ctx context.Context, call service.LLMCall) string {
	policy := s.tracePrivacyPolicy(ctx)
	action, rule := policy.Decide(service.TraceObservationFacts{
		WorkspaceID: call.WorkspaceID,
		UserID:      call.UserID,
		TokenID:     call.TokenID,
		Provider:    call.Provider,
		Model:       call.Model,
		Source:      call.Source,
	})
	inherited := s.traceSuppression(call.WorkspaceID, call.TraceID)
	if action == service.TracePrivacyNone {
		return inherited
	}
	if s.suppressTrace(call.WorkspaceID, call.TraceID, action) && service.StricterTracePrivacy(action, inherited) != inherited {
		// Earlier observations of this trace may already be stored.
		s.cleanupSuppressedTrace(ctx, call.WorkspaceID, call.TraceID, service.StricterTracePrivacy(action, inherited), rule)
	}
	return service.StricterTracePrivacy(action, inherited)
}

// cleanupSuppressedTrace applies an action to observations already written.
// It runs after a short delay so writes still in flight from this trace's
// earlier observations land first.
func (s *Server) cleanupSuppressedTrace(ctx context.Context, workspaceID, traceID, action, rule string) {
	store, ok := s.tracePrivacyStore()
	if !ok {
		return
	}
	go func() {
		time.Sleep(2 * time.Second)
		bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		refs, err := store.ApplyTracePrivacyToTrace(bg, workspaceID, traceID, action)
		if err != nil {
			slog.Warn("trace privacy cleanup failed", "trace_id", traceID, "rule", rule, "error", err.Error())
			return
		}
		s.removeTraceSpillFiles(refs)
	}()
}

// removeTraceSpillFiles deletes spill files left by observations a privacy
// action removed or redacted. Only files inside a known spill directory are
// touched, because the paths come from the database.
func (s *Server) removeTraceSpillFiles(refs []string) {
	for _, ref := range refs {
		clean := filepath.Clean(ref)
		if !filepath.IsAbs(clean) || !strings.Contains(clean, string(filepath.Separator)+llmAuditDumpDir+string(filepath.Separator)) {
			continue
		}
		if err := os.Remove(clean); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Debug("trace privacy: spill removal failed", "path", clean, "error", err.Error())
		}
	}
}

// redactLLMCall strips every content-bearing field from an observation.
func redactLLMCall(call *service.LLMCall) {
	call.Input, call.Output, call.ErrorMessage = "", "", ""
	call.RequestBody, call.ResponseBody = "", ""
	call.RequestRef, call.ResponseRef = "", ""
	call.RequestTruncated, call.ResponseTruncated = false, false
	if call.Trace != nil {
		trace := *call.Trace
		trace.Input, trace.Output = "", ""
		call.Trace = &trace
	}
	if call.Metadata == nil {
		call.Metadata = map[string]any{}
	}
	call.Metadata["redacted"] = true
}

// ─── HTTP API ───

func (s *Server) tracePrivacyAccess(w http.ResponseWriter, r *http.Request) (service.TracePrivacyStorer, bool) {
	store, ok := s.tracePrivacyStore()
	if !ok {
		httpResponse(w, "trace privacy store unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	if _, ok := service.AccessPrincipalFromContext(r.Context()); !ok {
		httpResponse(w, "workspace selection required", http.StatusForbidden)
		return nil, false
	}
	return store, true
}

func tracePrivacyError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrTracePrivacyRuleNotFound):
		httpResponse(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, service.ErrTracePrivacyInvalid):
		httpResponse(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, service.ErrAccessDenied):
		httpResponse(w, "workspace or platform administrator permission required", http.StatusForbidden)
	case errors.Is(err, service.ErrWorkspaceRequired):
		httpResponse(w, "workspace selection required", http.StatusBadRequest)
	default:
		slog.Error("trace privacy operation failed", "error", err.Error())
		httpResponse(w, "trace privacy operation failed", http.StatusServiceUnavailable)
	}
}

// ListTracePrivacyRulesAPI handles GET /api/v1/trace-privacy/rules.
func (s *Server) ListTracePrivacyRulesAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.tracePrivacyAccess(w, r)
	if !ok {
		return
	}
	rules, err := store.ListTracePrivacyRules(r.Context())
	if err != nil {
		tracePrivacyError(w, err)
		return
	}
	httpResponseJSON(w, map[string]any{"items": rules}, http.StatusOK)
}

func decodeTracePrivacyRule(w http.ResponseWriter, r *http.Request) (service.TracePrivacyRule, bool) {
	var rule service.TracePrivacyRule
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	if err := d.Decode(&rule); err != nil {
		httpResponse(w, "invalid trace privacy rule", http.StatusBadRequest)
		return rule, false
	}
	return rule, true
}

// CreateTracePrivacyRuleAPI handles POST /api/v1/trace-privacy/rules.
func (s *Server) CreateTracePrivacyRuleAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.tracePrivacyAccess(w, r)
	if !ok {
		return
	}
	rule, ok := decodeTracePrivacyRule(w, r)
	if !ok {
		return
	}
	rule.ID = ""
	saved, err := store.SaveTracePrivacyRule(r.Context(), rule)
	if err != nil {
		tracePrivacyError(w, err)
		return
	}
	s.invalidateTracePrivacy()
	httpResponseJSON(w, saved, http.StatusCreated)
}

// UpdateTracePrivacyRuleAPI handles PUT /api/v1/trace-privacy/rules/{id}.
func (s *Server) UpdateTracePrivacyRuleAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.tracePrivacyAccess(w, r)
	if !ok {
		return
	}
	rule, ok := decodeTracePrivacyRule(w, r)
	if !ok {
		return
	}
	rule.ID = r.PathValue("id")
	if rule.ID == "" {
		httpResponse(w, "rule id required", http.StatusBadRequest)
		return
	}
	saved, err := store.SaveTracePrivacyRule(r.Context(), rule)
	if err != nil {
		tracePrivacyError(w, err)
		return
	}
	s.invalidateTracePrivacy()
	httpResponseJSON(w, saved, http.StatusOK)
}

// DeleteTracePrivacyRuleAPI handles DELETE /api/v1/trace-privacy/rules/{id}.
func (s *Server) DeleteTracePrivacyRuleAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.tracePrivacyAccess(w, r)
	if !ok {
		return
	}
	if err := store.DeleteTracePrivacyRule(r.Context(), r.PathValue("id")); err != nil {
		tracePrivacyError(w, err)
		return
	}
	s.invalidateTracePrivacy()
	httpResponse(w, "deleted", http.StatusOK)
}

// ApplyTracePrivacyRuleAPI handles POST /api/v1/trace-privacy/rules/{id}/apply.
// `?dry_run=true` only counts. Applying is irreversible.
func (s *Server) ApplyTracePrivacyRuleAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.tracePrivacyAccess(w, r)
	if !ok {
		return
	}
	dryRun := r.URL.Query().Get("dry_run") == "true"
	res, err := store.ApplyTracePrivacyRule(r.Context(), r.PathValue("id"), dryRun)
	if err != nil {
		tracePrivacyError(w, err)
		return
	}
	if !dryRun {
		s.removeTraceSpillFiles(res.SpillRefs)
	}
	httpResponseJSON(w, res, http.StatusOK)
}

// TracePrivacySettingsAPI handles GET/PUT /api/v1/trace-privacy/settings.
// Reading is open to rule managers; writing needs a platform administrator
// (enforced by the store).
func (s *Server) TracePrivacySettingsAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.tracePrivacyAccess(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodGet {
		settings, err := store.GetTracePrivacySettings(r.Context())
		if err != nil {
			tracePrivacyError(w, err)
			return
		}
		httpResponseJSON(w, settings, http.StatusOK)
		return
	}
	var req service.TracePrivacySettings
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(&req); err != nil {
		httpResponse(w, "invalid trace privacy settings", http.StatusBadRequest)
		return
	}
	saved, err := store.SaveTracePrivacySettings(r.Context(), req)
	if err != nil {
		tracePrivacyError(w, err)
		return
	}
	s.invalidateTracePrivacy()
	httpResponseJSON(w, saved, http.StatusOK)
}

// tracePrivacyOptOutResponse is the account-level view: whether the
// installation lets accounts opt out, and whether this one has.
type tracePrivacyOptOutResponse struct {
	Allowed  bool `json:"allowed"`
	OptedOut bool `json:"opted_out"`
}

// TracePrivacyOptOutAPI handles GET/PUT /api/v1/trace-privacy/opt-out for the
// authenticated account. The owner is the signed-in subject, never a
// submitted user ID.
func (s *Server) TracePrivacyOptOutAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := identity.FromContext(r.Context())
	if id == nil || id.Subject == "" {
		httpResponse(w, "a signed-in account is required", http.StatusForbidden)
		return
	}
	store, ok := s.tracePrivacyStore()
	if !ok || s.userPrefStore == nil {
		httpResponse(w, "trace privacy store unavailable", http.StatusServiceUnavailable)
		return
	}
	settings, err := store.GetTracePrivacySettings(r.Context())
	if err != nil {
		tracePrivacyError(w, err)
		return
	}
	if r.Method == http.MethodPut {
		var req struct {
			OptedOut bool `json:"opted_out"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
			httpResponse(w, "invalid request", http.StatusBadRequest)
			return
		}
		if req.OptedOut && !settings.AllowUserOptOut {
			httpResponse(w, "this installation does not let accounts opt out of tracing", http.StatusForbidden)
			return
		}
		if req.OptedOut {
			err = s.userPrefStore.SetUserPreference(r.Context(), service.UserPreference{UserID: id.Subject, Key: service.TracePrivacyOptOutKey, Value: json.RawMessage("true")})
		} else {
			err = s.userPrefStore.DeleteUserPreference(r.Context(), id.Subject, service.TracePrivacyOptOutKey)
		}
		if err != nil {
			slog.Error("save trace opt-out failed", "error", err.Error())
			httpResponse(w, "could not save preference", http.StatusServiceUnavailable)
			return
		}
		s.invalidateTracePrivacy()
	}
	pref, err := s.userPrefStore.GetUserPreference(r.Context(), id.Subject, service.TracePrivacyOptOutKey)
	if err != nil {
		httpResponse(w, "could not load preference", http.StatusServiceUnavailable)
		return
	}
	optedOut := pref != nil && !pref.Secret && string(pref.Value) == "true"
	httpResponseJSON(w, tracePrivacyOptOutResponse{Allowed: settings.AllowUserOptOut, OptedOut: optedOut}, http.StatusOK)
}
