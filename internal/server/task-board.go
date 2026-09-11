package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

// The Kanban board layout is workspace data, edited through the same task
// capabilities that govern the cards it draws: reading needs tasks.read,
// changing it needs tasks.write.

func (s *Server) taskBoardStore(w http.ResponseWriter) (service.TaskBoardStorer, bool) {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return nil, false
	}
	store, ok := s.store.(service.TaskBoardStorer)
	if !ok {
		httpResponse(w, "task board storage unavailable", http.StatusServiceUnavailable)
		return nil, false
	}
	return store, true
}

func taskBoardError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidTaskBoard), errors.Is(err, service.ErrInvalidTaskStatus):
		httpResponse(w, err.Error(), http.StatusBadRequest)
	case errors.Is(err, service.ErrTaskBoardConflict):
		httpResponse(w, "the board was changed by someone else; reload and reapply your edit", http.StatusConflict)
	default:
		if workspaceBusinessError(w, err) {
			return
		}
		httpResponse(w, err.Error(), http.StatusInternalServerError)
	}
}

// taskBoardResponse carries the layout plus what it does not show, so a client
// never has to work out on its own that some work is missing from the board.
type taskBoardResponse struct {
	*service.TaskBoardSettings
	UncoveredStatuses []string `json:"uncovered_statuses,omitempty"`
	AvailableStatuses []string `json:"available_statuses"`
}

func writeTaskBoard(w http.ResponseWriter, board *service.TaskBoardSettings, status int) {
	httpResponseJSON(w, taskBoardResponse{
		TaskBoardSettings: board,
		UncoveredStatuses: board.UncoveredStatuses(),
		AvailableStatuses: service.TaskStatuses,
	}, status)
}

// GetTaskBoardAPI returns the workspace board, or the shipped default.
func (s *Server) GetTaskBoardAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.taskBoardStore(w)
	if !ok {
		return
	}
	board, err := store.GetTaskBoard(r.Context())
	if err != nil {
		taskBoardError(w, err)
		return
	}
	writeTaskBoard(w, board, http.StatusOK)
}

// SaveTaskBoardAPI replaces the layout. The caller echoes the version it read;
// a mismatch means someone else edited the board in the meantime.
func (s *Server) SaveTaskBoardAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.taskBoardStore(w)
	if !ok {
		return
	}
	var req struct {
		Version int64                     `json:"version"`
		Columns []service.TaskBoardColumn `json:"columns"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		httpResponse(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	board, err := store.SaveTaskBoard(r.Context(), req.Columns, req.Version, s.getUserEmail(r))
	if err != nil {
		taskBoardError(w, err)
		return
	}
	writeTaskBoard(w, board, http.StatusOK)
}

// ResetTaskBoardAPI drops the stored layout and returns the default, so the
// caller can render the result without a second request.
func (s *Server) ResetTaskBoardAPI(w http.ResponseWriter, r *http.Request) {
	store, ok := s.taskBoardStore(w)
	if !ok {
		return
	}
	if err := store.ResetTaskBoard(r.Context()); err != nil {
		taskBoardError(w, err)
		return
	}
	board, err := store.GetTaskBoard(r.Context())
	if err != nil {
		taskBoardError(w, err)
		return
	}
	writeTaskBoard(w, board, http.StatusOK)
}
