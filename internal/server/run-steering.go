package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
)

// runSteering lets a person add a message to a run that is already going
// (OpenCode's "steer"). The message is delivered at the run's next step
// boundary: after the current tool results, before the next model call, or
// once the model answers, in which case the run continues with it. State is
// in memory on the replica that owns the run, like the run's own cancel
// handle and stream replay; a steer that reaches another replica is refused
// so the caller can send it normally after the run.
type runSteering struct {
	mu    sync.Mutex
	runs  map[string]*steerQueue
	limit int
}

type steerQueue struct {
	messages []string
}

const (
	steerMaxQueued = 20
	steerMaxBytes  = 64 << 10
)

func newRunSteering() *runSteering {
	return &runSteering{runs: map[string]*steerQueue{}, limit: steerMaxQueued}
}

// Register marks key as running here; the returned function ends it and
// returns anything still queued (delivered by nobody), so the caller can
// persist or report it.
func (r *runSteering) Register(key string) func() []string {
	if r == nil {
		return func() []string { return nil }
	}
	q := &steerQueue{}
	r.mu.Lock()
	r.runs[key] = q
	r.mu.Unlock()
	return func() []string {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.runs[key] == q {
			delete(r.runs, key)
		}
		left := q.messages
		q.messages = nil
		return left
	}
}

// Push queues text for the run under key. It reports false when no run is
// active on this replica.
func (r *runSteering) Push(key, text string) (bool, error) {
	if r == nil {
		return false, nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return false, errSteerEmpty
	}
	if len(text) > steerMaxBytes {
		return false, errSteerTooLarge
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	q := r.runs[key]
	if q == nil {
		return false, nil
	}
	if len(q.messages) >= r.limit {
		return false, errSteerFull
	}
	q.messages = append(q.messages, text)
	return true, nil
}

// Drain takes every queued message for key.
func (r *runSteering) Drain(key string) []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	q := r.runs[key]
	if q == nil || len(q.messages) == 0 {
		return nil
	}
	out := q.messages
	q.messages = nil
	return out
}

type steerError string

func (e steerError) Error() string { return string(e) }

const (
	errSteerEmpty    = steerError("message is empty")
	errSteerTooLarge = steerError("message is larger than 64 KiB")
	errSteerFull     = steerError("too many queued messages; wait for the run to take them")
)

// SteerDeveloperSessionAPI handles POST /api/v1/developer-sessions/{id}/steer:
// add a message to the session's running turn. 409 means no run of this
// session is active on this server (send it as a normal prompt instead).
func (s *Server) SteerDeveloperSessionAPI(w http.ResponseWriter, r *http.Request) {
	store := s.developerSpaceStore(w)
	if store == nil {
		return
	}
	// Resolving the session is the ownership check: foreign sessions are 404.
	session, err := store.GetDeveloperSession(r.Context(), r.PathValue("id"))
	if err != nil {
		developerSpaceError(w, err)
		return
	}
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, steerMaxBytes+1024)).Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}
	queued, err := s.steering.Push("developer:"+session.ID, req.Prompt)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !queued {
		httpResponse(w, "no running turn to add the message to", http.StatusConflict)
		return
	}
	httpResponseJSON(w, map[string]any{"queued": true}, http.StatusAccepted)
}

// SteerChatSessionAPI handles POST /api/v1/chat/sessions/{id}/steer: add a
// message to the Sessions turn that is running. Ownership is checked like
// every session route; 409 means no turn of it runs on this server.
func (s *Server) SteerChatSessionAPI(w http.ResponseWriter, r *http.Request) {
	if s.chatSessionStore == nil {
		httpResponse(w, "chat session store not configured", http.StatusServiceUnavailable)
		return
	}
	session, status, message := s.chatSessionForRequest(r, r.PathValue("id"))
	if session == nil {
		httpResponse(w, message, status)
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, steerMaxBytes+1024)).Decode(&req); err != nil {
		httpResponse(w, "invalid request body", http.StatusBadRequest)
		return
	}
	queued, err := s.steering.Push("session:"+session.ID, req.Content)
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !queued {
		httpResponse(w, "no running turn to add the message to", http.StatusConflict)
		return
	}
	httpResponseJSON(w, map[string]any{"queued": true}, http.StatusAccepted)
}
