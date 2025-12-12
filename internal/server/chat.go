package server

import (
	"fmt"
	"net/http"
)

func (s *Server) Chat(w http.ResponseWriter, r *http.Request) {
	clientKey, messageChan := s.addClient()

	// prepare the header
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// prepare the flusher
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpResponse(w, "SSE not supported", http.StatusInternalServerError)
		return
	}

	// first value
	// s.TriggerInfo(r.Context())

	// vByte, _ := json.Marshal(s.getInfo())

	// fmt.Fprintf(w, "event: info\ndata: %s\n\n", string(vByte))
	flusher.Flush()

	for {
		select {
		case message := <-messageChan:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", message.Type, message.Value)
			flusher.Flush()
		case <-r.Context().Done():
			s.deleteClient(clientKey)
			return
		}
	}
}
