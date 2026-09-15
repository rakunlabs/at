package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/oklog/ulid/v2"

	hostterminal "github.com/rakunlabs/at/internal/service/terminal"
)

// A WebSocket is an attachment, never the lifetime owner of a shell.
func (s *Server) TerminalWebSocketAPI(w http.ResponseWriter, r *http.Request) {
	m, req := s.terminalAccess(w, r)
	if m == nil {
		return
	}
	// Require an explicit configured browser Origin even for this GET upgrade.
	auth := nativeRuntimeFromRequest(r, s.nativeAuth)
	if auth == nil || r.Header.Get("Origin") == "" {
		nativeError(w, 403, "configured browser Origin required")
		return
	}
	if !auth.sameOrigin(w, r) {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	setupCtx, setupCancel := context.WithTimeout(ctx, 10*time.Second)
	defer setupCancel()
	item, err := m.store.GetTerminal(setupCtx, req.Owner, r.PathValue("id"))
	if err != nil {
		nativeError(w, 503, err.Error())
		return
	}
	if item == nil {
		nativeError(w, 404, "terminal not found")
		return
	}
	peer, err := m.target(setupCtx, req, item.TargetID)
	if err != nil {
		nativeError(w, 503, err.Error())
		return
	}
	upgrader := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 16384, CheckOrigin: func(*http.Request) bool { return true }} // validated above against configured origin
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(32768)
	_ = ws.SetReadDeadline(time.Now().Add(40 * time.Second))
	ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(40 * time.Second)) })
	var writeMu sync.Mutex
	write := func(kind int, data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return ws.WriteMessage(kind, data)
	}
	message := func(kind, text string) {
		data, _ := json.Marshal(map[string]string{"type": kind, "message": text})
		_ = write(websocket.TextMessage, data)
	}
	req.ID, req.Link, req.Cols, req.Rows = item.ID, ulid.Make().String(), 120, 30
	var output io.ReadCloser
	var local *hostterminal.Attachment
	if peer == nil {
		if err := m.authorized(setupCtx, req); err != nil {
			message("error", err.Error())
			return
		}
		local, err = hostterminal.Attach(setupCtx, item.ID, item.Username, req.Cols, req.Rows)
		if err != nil {
			message("error", err.Error())
			return
		}
		defer local.Close()
		output = local.File
	} else {
		reader, writer := io.Pipe()
		m.mu.Lock()
		m.outputs[req.Link] = terminalOutput{peer: peer.String(), writer: writer}
		m.mu.Unlock()
		defer func() {
			reader.Close()
			writer.Close()
			m.mu.Lock()
			delete(m.outputs, req.Link)
			m.mu.Unlock()
			detach := req
			detach.Op = "detach"
			endCtx, done := context.WithTimeout(context.Background(), 3*time.Second)
			defer done()
			_, _ = m.call(endCtx, peer, detach)
		}()
		req.Op = "attach"
		if _, err := m.call(setupCtx, peer, req); err != nil {
			message("error", err.Error())
			return
		}
		output = reader
	}
	message("ready", "Connected")
	// All goroutines are bounded by the attachment context. Closing the local
	// PTY kills only tmux's attached client, not its independent system service.
	defer output.Close()
	go func() {
		<-ctx.Done()
		ws.Close()
		output.Close()
	}()
	go func() {
		defer cancel()
		buf := make([]byte, 16384)
		for {
			n, err := output.Read(buf)
			if n > 0 {
				if write(websocket.BinaryMessage, buf[:n]) != nil {
					return
				}
			}
			if err != nil {
				message("closed", "Disconnected. Reconnect to resume, or start a new shell if this session has ended.")
				return
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.ctx.Done():
				return
			case <-ticker.C:
				checkCtx, done := context.WithTimeout(ctx, 5*time.Second)
				err := m.authorized(checkCtx, req)
				if err == nil && peer != nil {
					heartbeat := req
					heartbeat.Op = "heartbeat"
					_, err = m.call(checkCtx, peer, heartbeat)
				}
				done()
				if err != nil {
					message("error", err.Error())
					return
				}
				if write(websocket.PingMessage, nil) != nil {
					return
				}
			}
		}
	}()
	for {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		input := req
		switch kind {
		case websocket.BinaryMessage:
			if len(data) > 16384 {
				message("error", "Input chunk too large")
				return
			}
			input.Op, input.Data = "input", data
		case websocket.TextMessage:
			var size struct {
				Type string `json:"type"`
				Cols uint16 `json:"cols"`
				Rows uint16 `json:"rows"`
			}
			if json.Unmarshal(data, &size) != nil || size.Type != "resize" || !terminalSizeValid(size.Cols, size.Rows) {
				message("error", "Invalid resize message")
				return
			}
			input.Op, input.Cols, input.Rows = "resize", size.Cols, size.Rows
		default:
			continue
		}
		callCtx, done := context.WithTimeout(ctx, 5*time.Second)
		if local != nil {
			err = m.authorized(callCtx, req)
			if err == nil {
				if input.Op == "resize" {
					err = local.Resize(input.Cols, input.Rows)
				} else {
					_ = local.File.SetWriteDeadline(time.Now().Add(5 * time.Second))
					_, err = local.File.Write(input.Data)
				}
			}
		} else {
			_, err = m.call(callCtx, peer, input)
		}
		done()
		if err != nil {
			message("error", fmt.Sprintf("Terminal connection: %v", err))
			return
		}
	}
}
