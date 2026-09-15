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
	// The browser is told its role rather than inferring it from silence: a
	// watcher whose keystrokes were dropped would otherwise look like a frozen
	// shell. Repeats are cheap and keep the two sides in step after a handover.
	var roleMu sync.Mutex
	lastRole, lastWatchers := false, -1
	role := func(control bool, watchers int, force bool) {
		roleMu.Lock()
		defer roleMu.Unlock()
		if !force && control == lastRole && watchers == lastWatchers {
			return
		}
		lastRole, lastWatchers = control, watchers
		data, _ := json.Marshal(map[string]any{"type": "role", "control": control, "watchers": watchers})
		_ = write(websocket.TextMessage, data)
	}
	req.ID, req.Link, req.Cols, req.Rows = item.ID, ulid.Make().String(), 120, 30
	var output io.ReadCloser
	var local *hostterminal.Attachment
	// seat is set only when this node owns the shell. Remote connections read
	// their role from RPC replies, because the registry lives with the tmux
	// socket and not on the node the browser reached.
	var seat *terminalSeat
	remoteControl, remoteWatchers := false, 0
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
		seat = m.joinSeat(req.Link, item.ID, req.Owner)
		defer m.leaveSeat(req.Link)
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
		reply, err := m.call(setupCtx, peer, req)
		if err != nil {
			message("error", err.Error())
			return
		}
		remoteControl, remoteWatchers = reply.Control, reply.Watchers
		output = reader
	}
	message("ready", "Connected")
	if seat != nil {
		control, watchers := m.seatState(req.Link)
		role(control, watchers, true)
	} else {
		role(remoteControl, remoteWatchers, true)
	}
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
		// A local connection is woken the moment control moves. A remote one
		// learns from the heartbeat reply, so it can lag by one interval; input
		// is gated on the host either way, so the lag only delays the badge.
		var handover <-chan struct{}
		if seat != nil {
			handover = seat.notified
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-m.ctx.Done():
				return
			case <-handover:
				control, watchers := m.seatState(req.Link)
				role(control, watchers, false)
			case <-ticker.C:
				checkCtx, done := context.WithTimeout(ctx, 5*time.Second)
				err := m.authorized(checkCtx, req)
				if err == nil && peer != nil {
					heartbeat := req
					heartbeat.Op = "heartbeat"
					var reply terminalReply
					if reply, err = m.call(checkCtx, peer, heartbeat); err == nil {
						role(reply.Control, reply.Watchers, false)
					}
				}
				done()
				if err != nil {
					message("error", err.Error())
					return
				}
				if seat != nil {
					control, watchers := m.seatState(req.Link)
					role(control, watchers, false)
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
			// "control" carries the size too: the arriving holder resizes the
			// shared window to its own screen as it takes over.
			if json.Unmarshal(data, &size) != nil || (size.Type != "resize" && size.Type != "control") || !terminalSizeValid(size.Cols, size.Rows) {
				message("error", "Invalid terminal message")
				return
			}
			input.Op, input.Cols, input.Rows = size.Type, size.Cols, size.Rows
		default:
			continue
		}
		callCtx, done := context.WithTimeout(ctx, 5*time.Second)
		if local != nil {
			err = m.authorized(callCtx, req)
			if err == nil && input.Op == "control" {
				err = m.takeSeat(req.Link)
			}
			if err == nil {
				control, watchers := m.seatState(req.Link)
				switch {
				case input.Op == "resize":
					err = local.Resize(input.Cols, input.Rows)
					if control {
						hostterminal.ResizeWindow(callCtx, item.ID, input.Cols, input.Rows)
					}
				case input.Op == "control":
					if control {
						hostterminal.ResizeWindow(callCtx, item.ID, input.Cols, input.Rows)
					}
				case !control:
					// Watching: the keystroke is discarded rather than closing
					// the connection, and the browser is re-told its role.
				default:
					_ = local.File.SetWriteDeadline(time.Now().Add(5 * time.Second))
					_, err = local.File.Write(input.Data)
				}
				role(control, watchers, input.Op == "control")
			}
		} else {
			var reply terminalReply
			reply, err = m.call(callCtx, peer, input)
			if err == nil {
				role(reply.Control, reply.Watchers, input.Op == "control")
			}
		}
		done()
		if err != nil {
			message("error", fmt.Sprintf("Terminal connection: %v", err))
			return
		}
	}
}
