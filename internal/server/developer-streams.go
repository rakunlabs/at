package server

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

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
		ctx, cancel := context.WithCancel(service.ContextWithDeveloperRun(r.Context(), runID))
		done, stopped := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(stopped)
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case <-ticker.C:
					check, stop := context.WithTimeout(ctx, 5*time.Second)
					err := runs.HeartbeatDeveloperRun(check, r.PathValue("id"), runID)
					stop()
					if err != nil {
						cancel()
						return
					}
				}
			}
		}()
		defer func() {
			close(done)
			cancel()
			<-stopped
			finish, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer stop()
			if err := runs.ReleaseDeveloperRun(finish, r.PathValue("id"), runID); err != nil {
				slog.Error("developer run release failed", "session", r.PathValue("id"), "error", err.Error())
			}
		}()
		handler(w, r.WithContext(ctx))
	}, true)
}

func developerStreamMatches(stream *chatStream, session *service.DeveloperSession) bool {
	return stream.developer && stream.session == session.ID && stream.owner == session.OwnerUserID && stream.workspace == session.WorkspaceID
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
	id := ""
	s.chatStreams.mu.Lock()
	for key, stream := range s.chatStreams.streams {
		stream.mu.Lock()
		active := developerStreamMatches(stream, session) && !stream.done
		stream.mu.Unlock()
		if active {
			id = key
			break
		}
	}
	s.chatStreams.mu.Unlock()
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
	s.chatStreams.mu.Lock()
	stream := s.chatStreams.streams[r.PathValue("stream")]
	s.chatStreams.mu.Unlock()
	if stream == nil || !developerStreamMatches(stream, session) {
		httpResponse(w, "live run unavailable on this server; check saved history, do not resend automatically", http.StatusNotFound)
		return
	}
	stream.mu.Lock()
	expired := stream.done && time.Now().After(stream.expires)
	stream.mu.Unlock()
	if expired {
		httpResponse(w, "live run replay expired; check saved history", http.StatusGone)
		return
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || offset < 0 {
		httpResponse(w, "invalid stream offset", http.StatusBadRequest)
		return
	}
	s.replayChatStream(w, r, stream, offset)
}

func (s *Server) cancelDeveloperStreams(session *service.DeveloperSession) {
	s.chatStreams.mu.Lock()
	defer s.chatStreams.mu.Unlock()
	for _, stream := range s.chatStreams.streams {
		if developerStreamMatches(stream, session) {
			stream.cancel()
		}
	}
}
