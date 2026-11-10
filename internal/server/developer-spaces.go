package server

import (
	"errors"
	"net/http"

	"github.com/rakunlabs/at/internal/service"
)

func (s *Server) developerSpaceStore(w http.ResponseWriter) service.DeveloperSpaceStorer {
	if s.store == nil {
		httpResponse(w, "store not configured", http.StatusServiceUnavailable)
		return nil
	}
	store, ok := s.store.(service.DeveloperSpaceStorer)
	if !ok {
		httpResponse(w, "developer spaces unavailable", http.StatusServiceUnavailable)
		return nil
	}
	return store
}

func developerSpaceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrDeveloperSpaceNotFound):
		httpResponse(w, "developer space not found", http.StatusNotFound)
	case errors.Is(err, service.ErrDeveloperSpaceBusy):
		httpResponse(w, err.Error(), http.StatusConflict)
	case errors.Is(err, service.ErrAccessDenied):
		httpResponse(w, "developer space access denied", http.StatusForbidden)
	default:
		httpResponse(w, err.Error(), http.StatusInternalServerError)
	}
}
