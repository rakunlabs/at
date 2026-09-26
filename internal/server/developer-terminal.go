package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/rakunlabs/at/internal/service/container"
)

func (s *Server) DeveloperWorktreeTerminalAPI(w http.ResponseWriter, r *http.Request) {
	_, worktree, cfg, scope, ok := s.developerWorktreeCommand(w, r)
	if !ok {
		return
	}
	auth := nativeRuntimeFromRequest(r, s.nativeAuth)
	if auth == nil || r.Header.Get("Origin") == "" {
		nativeError(w, http.StatusForbidden, "configured browser Origin required")
		return
	}
	if !auth.sameOrigin(w, r) {
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
	file, closeShell, err := s.containerManager.AttachShell(ctx, scope, cfg, developerWorktreePath(worktree.ID), 120, 30)
	if err != nil {
		_ = ws.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
		return
	}
	defer closeShell() //nolint:errcheck
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
			n, err := file.Read(buf)
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
				_, _ = file.Write(data)
			}
		case websocket.TextMessage:
			var message struct {
				Type string `json:"type"`
				Cols uint16 `json:"cols"`
				Rows uint16 `json:"rows"`
			}
			if json.Unmarshal(data, &message) == nil && message.Type == "resize" && message.Cols >= 2 && message.Rows >= 2 {
				_ = container.ResizePTY(file, message.Cols, message.Rows)
			}
		}
	}
}
