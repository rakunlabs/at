package server

import (
	"context"
	"net/http"

	"github.com/oklog/ulid/v2"

	"github.com/rakunlabs/at/internal/service"
)

// The browser is an observer, not the owner of a coding run. Reuse the bounded
// replay transport, but admit developer sessions through their own owner scope.
func (s *Server) startDeveloperStream(w http.ResponseWriter, r *http.Request, handler http.HandlerFunc) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	if _, err := store.GetDeveloperSession(r.Context(), r.PathValue("id")); err != nil {
		developerSpaceError(w, err)
		return
	}
	if r.Header.Get("X-AT-Stream-ID") == "" {
		r.Header.Set("X-AT-Stream-ID", ulid.Make().String())
	}
	// A replay ID is client-selected; ownership must never reuse that ID (ABA).
	runID := ulid.Make().String()
	s.startReplayStream(w, r, func(w http.ResponseWriter, r *http.Request) {
		runs, ok := s.store.(service.DeveloperRunStorer)
		if !ok {
			httpResponse(w, "durable developer run store unavailable", http.StatusServiceUnavailable)
			return
		}
		if err := runs.AcquireDeveloperRun(r.Context(), r.PathValue("id"), runID); err != nil {
			developerSpaceError(w, err)
			return
		}
		// A stale receipt is never taken over here: a crashed owner may still
		// be running commands in the container. Any renewal failure stops it.
		ctx, release := holdRunReceipt(service.ContextWithDeveloperRun(r.Context(), runID), runReceipt{
			Label:   "developer:" + r.PathValue("id"),
			Renew:   func(ctx context.Context) error { return runs.HeartbeatDeveloperRun(ctx, r.PathValue("id"), runID) },
			Release: func(ctx context.Context) error { return runs.ReleaseDeveloperRun(ctx, r.PathValue("id"), runID) },
		})
		defer release()
		if s.containerManager != nil {
			runtimeCtx, finish, err := s.containerManager.BeginRuntimeRun(ctx)
			if err != nil {
				httpResponse(w, err.Error(), http.StatusConflict)
				return
			}
			defer finish()
			ctx = runtimeCtx
		}
		handler(w, r.WithContext(ctx))
	}, replayDeveloper)
}

func developerStreams(session *service.DeveloperSession) replayStreamMatch {
	return replayStreamMatch{kind: replayDeveloper, session: session.ID, owner: session.OwnerUserID, workspace: session.WorkspaceID}
}

// Discovery reports expired ownership as interrupted but never clears its
// exclusion receipt, relaunches work, or guesses that tools are safe to retry.
func (s *Server) GetDeveloperActiveStreamAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	session, err := store.GetDeveloperSession(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	var durable *service.DeveloperRun
	if runs, ok := s.store.(service.DeveloperRunStorer); ok {
		durable, err = runs.GetDeveloperRun(r.Context(), session.ID)
		if err != nil {
			developerSpaceError(w, err)
			return
		}
		session, err = store.GetDeveloperSession(r.Context(), session.ID)
		if err != nil {
			developerSpaceError(w, err)
			return
		}
	}
	id := s.activeReplayStream(developerStreams(session))
	httpResponseJSON(w, map[string]any{"stream_id": id, "session": session, "run": durable}, http.StatusOK)
}

func (s *Server) DeveloperStreamAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	session, err := store.GetDeveloperSession(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	s.replayRunStream(w, r, developerStreams(session))
}

func (s *Server) cancelDeveloperStreams(session *service.DeveloperSession) {
	s.cancelReplayStreams(developerStreams(session))
}
