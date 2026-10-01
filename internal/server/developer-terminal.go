package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/rakunlabs/at/internal/nativeauth"
	"github.com/rakunlabs/at/internal/service"
)

// DeveloperSpaceTerminalAPI attaches an interactive shell inside the caller's
// own space container, starting in ?cwd= (a folder relative to /workspace).
// Each connection is its own shell; closing it never stops the container.
func (s *Server) DeveloperSpaceTerminalAPI(w http.ResponseWriter, r *http.Request) {
	cwd, err := service.CleanDeveloperPath(r.URL.Query().Get("cwd"))
	if err != nil {
		httpResponse(w, err.Error(), http.StatusBadRequest)
		return
	}
	auth := nativeauth.RuntimeFromRequest(r, s.nativeAuth)
	if auth == nil || r.Header.Get("Origin") == "" {
		nativeError(w, http.StatusForbidden, "configured browser Origin required")
		return
	}
	if !auth.SameOrigin(w, r) {
		return
	}
	h, ok := s.developerRuntime(w, r)
	if !ok {
		return
	}
	upgrader := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 16384, CheckOrigin: func(*http.Request) bool { return true }}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	// A folder deleted since the tab was opened falls back to the space root
	// rather than failing the attach with an opaque docker error.
	workDir := service.DeveloperAbsolutePath(cwd)
	if cwd != "" {
		if _, _, code, err := h.exec(ctx, s, service.DeveloperWorkspaceRoot, "test", "-d", workDir); err != nil || code != 0 {
			workDir = service.DeveloperWorkspaceRoot
		}
	}
	term, err := s.containerManager.AttachShell(ctx, h.scope, h.cfg, workDir, 120, 30)
	if err != nil {
		_ = ws.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
		return
	}
	defer term.Close() //nolint:errcheck
	_, _ = h.store.SetDeveloperSpaceRuntime(context.WithoutCancel(ctx), h.space.ID, service.DeveloperSpaceReady, "")
	var writeMu sync.Mutex
	write := func(kind int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return ws.WriteMessage(kind, data)
	}
	_ = ws.WriteJSON(map[string]string{"type": "ready", "message": "Connected"})
	go func() {
		buf := make([]byte, 16384)
		for {
			n, err := term.Read(buf)
			if n > 0 && write(websocket.BinaryMessage, buf[:n]) != nil {
				cancel()
				return
			}
			if err != nil {
				cancel()
				return
			}
		}
	}()
	ws.SetReadLimit(32768)
	for {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		switch kind {
		case websocket.BinaryMessage:
			if len(data) <= 16384 {
				_, _ = term.Write(data)
			}
		case websocket.TextMessage:
			var message struct {
				Type string `json:"type"`
				Cols uint16 `json:"cols"`
				Rows uint16 `json:"rows"`
			}
			if json.Unmarshal(data, &message) == nil && message.Type == "resize" && message.Cols >= 2 && message.Rows >= 2 {
				_ = term.Resize(message.Cols, message.Rows)
			}
		}
	}
}
