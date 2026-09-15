package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/ada/middleware/auth/identity"
	"github.com/rakunlabs/ada/middleware/auth/issuer"
	"github.com/rakunlabs/alan"

	"github.com/rakunlabs/at/internal/service"
	hostterminal "github.com/rakunlabs/at/internal/service/terminal"
)

const terminalRPCType = "at.terminal.v1.rpc"
const terminalOutputType = "at.terminal.v1.output"

type terminalRPC struct {
	Op      string `json:"op"`
	Owner   string `json:"owner"`
	Session string `json:"session"`
	ID      string `json:"id,omitempty"`
	Link    string `json:"link,omitempty"`
	Data    []byte `json:"data,omitempty"`
	Cols    uint16 `json:"cols,omitempty"`
	Rows    uint16 `json:"rows,omitempty"`
}

type terminalReply struct {
	Error string              `json:"error,omitempty"`
	Host  *hostterminal.Host  `json:"host,omitempty"`
	Users []hostterminal.User `json:"users,omitempty"`
	Alive bool                `json:"alive"`
	// Control and Watchers travel back on every attached operation so a
	// connection that lost control finds out from its next call rather than
	// silently typing into nothing.
	Control  bool `json:"control,omitempty"`
	Watchers int  `json:"watchers,omitempty"`
}

type terminalLink struct {
	attachment *hostterminal.Attachment
	peer       string
	owner      string
	session    string
	id         string
	lastSeen   time.Time
	cancel     context.CancelFunc
}

type terminalOutput struct {
	peer   string
	writer *io.PipeWriter
}

// A seat is one attached connection to one terminal. Exactly one seat per
// terminal holds control and is the only one whose bytes reach the shell; the
// rest watch. The registry lives on the host that owns the tmux socket, never on
// the node a browser happens to reach, because two browsers can arrive through
// two different replicas and a per-replica flag would hand out two writers.
type terminalSeat struct {
	id       string
	owner    string
	control  bool
	joined   time.Time
	notified chan struct{}
}

// notify wakes a local connection that has just gained or lost control, so it
// does not have to wait for its next heartbeat to find out. Remote connections
// learn from the reply to their next call instead.
func (s *terminalSeat) notify() {
	select {
	case s.notified <- struct{}{}:
	default:
	}
}

// joinSeat registers a connection and grants control only when the terminal has
// nobody in control, so arriving never takes a running shell away from whoever
// is typing in it. Owner is carried for the assertion in takeSeat: seats are
// per-user today, and control must not cross accounts if sharing is added.
func (m *terminalManager) joinSeat(link, id, owner string) *terminalSeat {
	m.mu.Lock()
	defer m.mu.Unlock()
	seat := &terminalSeat{id: id, owner: owner, joined: time.Now(), notified: make(chan struct{}, 1)}
	seat.control = true
	for _, other := range m.seats {
		if other.id == id && other.control {
			seat.control = false
			break
		}
	}
	m.seats[link] = seat
	return seat
}

// leaveSeat hands control to the connection that has been waiting longest, so a
// terminal is never left watchable but unusable after the holder closes a tab.
func (m *terminalManager) leaveSeat(link string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seat, ok := m.seats[link]
	delete(m.seats, link)
	if !ok || !seat.control {
		return
	}
	var next *terminalSeat
	for _, other := range m.seats {
		if other.id == seat.id && (next == nil || other.joined.Before(next.joined)) {
			next = other
		}
	}
	if next != nil {
		next.control = true
		next.notify()
	}
}

// takeSeat is the explicit handover. It is the only way control moves while the
// holder is still connected.
func (m *terminalManager) takeSeat(link string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	seat, ok := m.seats[link]
	if !ok {
		return fmt.Errorf("terminal connection ended")
	}
	if seat.control {
		return nil
	}
	for other, candidate := range m.seats {
		if other == link || candidate.id != seat.id || !candidate.control {
			continue
		}
		if candidate.owner != seat.owner {
			return fmt.Errorf("this terminal is controlled by another account")
		}
		candidate.control = false
		candidate.notify()
	}
	seat.control = true
	return nil
}

// seatState reports what the browser shows: whether this connection may type and
// how many others are watching the same terminal.
func (m *terminalManager) seatState(link string) (bool, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seat, ok := m.seats[link]
	if !ok {
		return false, 0
	}
	others := 0
	for other, candidate := range m.seats {
		if other != link && candidate.id == seat.id {
			others++
		}
	}
	return seat.control, others
}

type terminalManager struct {
	ctx       context.Context
	store     service.TerminalStorer
	auth      service.AuthStorer
	transport *alan.Alan
	host      hostterminal.Host
	mu        sync.Mutex
	links     map[string]*terminalLink
	outputs   map[string]terminalOutput
	seats     map[string]*terminalSeat
}

func (s *Server) initTerminals() {
	store, ok := s.store.(service.TerminalStorer)
	if !ok {
		return
	}
	auth, ok := s.store.(service.AuthStorer)
	if !ok {
		return
	}
	m := &terminalManager{ctx: s.ctx, store: store, auth: auth, host: hostterminal.LocalHost(), transport: s.cluster.TerminalTransport(), links: map[string]*terminalLink{}, outputs: map[string]terminalOutput{}, seats: map[string]*terminalSeat{}}
	s.terminals = m
	m.registerTransport()
}

func (m *terminalManager) registerTransport() {
	if m.transport != nil {
		m.transport.Handle(terminalRPCType, func(ctx context.Context, msg alan.Message) {
			var req terminalRPC
			var reply terminalReply
			if len(msg.Data) > 32768 || json.Unmarshal(msg.Data, &req) != nil {
				reply.Error = "invalid terminal request"
			} else {
				reply = m.local(ctx, req, msg.Addr)
			}
			data, _ := json.Marshal(reply)
			_, _ = m.transport.Reply(ctx, msg, data)
		})
		m.transport.HandleStream(terminalOutputType, func(ctx context.Context, msg alan.Message, body io.Reader) error {
			reader := bufio.NewReaderSize(body, 128)
			link, err := reader.ReadSlice('\n')
			if err != nil || len(link) != 27 {
				return fmt.Errorf("invalid terminal stream header")
			}
			m.mu.Lock()
			out, ok := m.outputs[string(link[:26])]
			m.mu.Unlock()
			if !ok || out.peer != msg.Addr.String() {
				return fmt.Errorf("unrecognized terminal stream")
			}
			_, err = io.Copy(out.writer, reader)
			_ = out.writer.CloseWithError(err)
			return err
		})
	}
}

func (m *terminalManager) authorized(ctx context.Context, req terminalRPC) error {
	if req.Owner == "" || req.Session == "" {
		return fmt.Errorf("administrator session required")
	}
	u, expires, err := m.auth.ResolveAuthSession(ctx, req.Session)
	if err != nil {
		return fmt.Errorf("validate terminal session: %w", err)
	}
	if u == nil || u.ID != req.Owner || !u.Admin || u.Disabled || !expires.After(time.Now()) {
		return fmt.Errorf("administrator session expired or revoked")
	}
	return nil
}

func (m *terminalManager) local(ctx context.Context, req terminalRPC, peer *net.UDPAddr) terminalReply {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	reply, err := m.execute(ctx, req, peer)
	if err != nil {
		reply.Error = err.Error()
	}
	return reply
}

func (m *terminalManager) execute(ctx context.Context, req terminalRPC, peer *net.UDPAddr) (terminalReply, error) {
	result := terminalReply{}
	if err := m.authorized(ctx, req); err != nil {
		return result, err
	}
	if req.Op == "host" {
		h := m.host
		result.Host = &h
		return result, nil
	}
	if req.Op == "users" {
		users, err := hostterminal.Users(ctx)
		result.Users = users
		return result, err
	}
	item, err := m.store.GetTerminal(ctx, req.Owner, req.ID)
	if err != nil {
		return result, err
	}
	if item == nil || item.TargetID != m.host.ID {
		return result, fmt.Errorf("terminal not found on this host")
	}
	switch req.Op {
	case "start":
		err = m.store.OperateTerminal(ctx, req.Owner, item.ID, false, func(current service.TerminalSession) error {
			if current.TargetID != m.host.ID {
				return fmt.Errorf("terminal target changed")
			}
			return hostterminal.Start(ctx, current.ID, current.Username)
		})
		if err == nil {
			slog.Info("terminal started", "owner", req.Owner, "terminal", item.ID, "target", item.TargetID, "linux_user", item.Username)
		}
	case "status":
		result.Alive = hostterminal.Alive(ctx, item.ID)
	case "stop":
		err = m.store.OperateTerminal(ctx, req.Owner, item.ID, true, func(current service.TerminalSession) error {
			if current.TargetID != m.host.ID {
				return fmt.Errorf("terminal target changed")
			}
			return hostterminal.Stop(ctx, current.ID)
		})
		if err == nil {
			slog.Info("terminal stopped", "owner", req.Owner, "terminal", item.ID, "target", item.TargetID)
		}
	case "attach":
		if peer == nil || m.transport == nil {
			return result, fmt.Errorf("remote terminal transport unavailable")
		}
		if _, err := ulid.ParseStrict(req.Link); err != nil {
			return result, fmt.Errorf("invalid connection ID")
		}
		if !terminalSizeValid(req.Cols, req.Rows) {
			return result, fmt.Errorf("invalid terminal size")
		}
		m.mu.Lock()
		if len(m.links) >= 128 {
			m.mu.Unlock()
			return result, fmt.Errorf("too many terminal connections")
		}
		if _, exists := m.links[req.Link]; exists {
			m.mu.Unlock()
			return result, fmt.Errorf("connection already exists")
		}
		m.mu.Unlock()
		a, err := hostterminal.Attach(ctx, item.ID, item.Username, req.Cols, req.Rows)
		if err != nil {
			return result, err
		}
		linkCtx, cancel := context.WithCancel(m.ctx)
		link := &terminalLink{attachment: a, peer: peer.String(), owner: req.Owner, session: req.Session, id: req.ID, lastSeen: time.Now(), cancel: cancel}
		m.mu.Lock()
		m.links[req.Link] = link
		m.mu.Unlock()
		m.joinSeat(req.Link, item.ID, req.Owner)
		result.Control, result.Watchers = m.seatState(req.Link)
		go func() {
			defer cancel()
			defer a.Close()
			defer m.leaveSeat(req.Link)
			defer func() { m.mu.Lock(); delete(m.links, req.Link); m.mu.Unlock() }()
			_, _ = m.transport.SendToStream(linkCtx, peer, terminalOutputType, io.MultiReader(strings.NewReader(req.Link+"\n"), a.File))
		}()
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			defer a.Close()
			for {
				select {
				case <-linkCtx.Done():
					return
				case <-ticker.C:
					m.mu.Lock()
					stale := time.Since(link.lastSeen) > 35*time.Second
					m.mu.Unlock()
					checkCtx, done := context.WithTimeout(linkCtx, 5*time.Second)
					err := m.authorized(checkCtx, req)
					done()
					if stale || err != nil {
						cancel()
						return
					}
				}
			}
		}()
	case "input", "resize", "detach", "heartbeat", "control":
		m.mu.Lock()
		link, ok := m.links[req.Link]
		if !ok || peer == nil || link.peer != peer.String() || link.owner != req.Owner || link.session != req.Session || link.id != req.ID {
			m.mu.Unlock()
			return result, fmt.Errorf("terminal connection ended")
		}
		link.lastSeen = time.Now()
		m.mu.Unlock()
		if req.Op == "control" {
			if err := m.takeSeat(req.Link); err != nil {
				return result, err
			}
		}
		control, watchers := m.seatState(req.Link)
		result.Control, result.Watchers = control, watchers
		switch req.Op {
		case "input":
			if len(req.Data) > 16384 {
				return result, fmt.Errorf("terminal input too large")
			}
			// Dropped, not refused: the caller is told it is watching and keeps
			// its connection instead of being disconnected for a stray keystroke.
			if !control {
				return result, nil
			}
			_ = link.attachment.File.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, err = link.attachment.File.Write(req.Data)
		case "resize", "control":
			// Only the holder sets the shared window; a watcher resizes its own
			// viewport so its screen stays readable without reflowing the shell.
			if req.Op == "resize" {
				err = link.attachment.Resize(req.Cols, req.Rows)
			}
			if control && terminalSizeValid(req.Cols, req.Rows) {
				hostterminal.ResizeWindow(ctx, item.ID, req.Cols, req.Rows)
			}
		case "detach":
			link.cancel()
			link.attachment.Close()
		}
	default:
		err = fmt.Errorf("unknown terminal operation")
	}
	return result, err
}

func terminalSizeValid(cols, rows uint16) bool {
	return cols >= 2 && cols <= 500 && rows >= 2 && rows <= 300
}

func (m *terminalManager) targets(ctx context.Context, req terminalRPC) ([]hostterminal.Host, map[string]*net.UDPAddr) {
	hosts := []hostterminal.Host{m.host}
	peers := map[string]*net.UDPAddr{}
	if m.transport == nil {
		return hosts, peers
	}
	req.Op = "host"
	data, _ := json.Marshal(req)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	replies, _ := m.transport.SendAndWaitReply(ctx, terminalRPCType, data)
	for _, response := range replies {
		var reply terminalReply
		if json.Unmarshal(response.Data, &reply) != nil || reply.Error != "" || reply.Host == nil || reply.Host.ID == "" || reply.Host.ID == m.host.ID {
			continue
		}
		if _, exists := peers[reply.Host.ID]; exists {
			continue
		}
		hosts = append(hosts, *reply.Host)
		peers[reply.Host.ID] = response.Addr
	}
	return hosts, peers
}

func (m *terminalManager) call(ctx context.Context, peer *net.UDPAddr, req terminalRPC) (terminalReply, error) {
	if peer == nil {
		return m.execute(ctx, req, nil)
	}
	if m.transport == nil {
		return terminalReply{}, fmt.Errorf("terminal cluster transport unavailable; configure Alan cluster admission")
	}
	data, err := json.Marshal(req)
	if err != nil {
		return terminalReply{}, err
	}
	response, err := m.transport.SendToAndWaitReply(ctx, peer, terminalRPCType, data)
	if err != nil {
		return terminalReply{}, fmt.Errorf("terminal target unavailable: %w", err)
	}
	var reply terminalReply
	if err := json.Unmarshal(response.Data, &reply); err != nil {
		return reply, fmt.Errorf("decode terminal reply: %w", err)
	}
	if reply.Error != "" {
		return reply, errors.New(reply.Error)
	}
	return reply, nil
}

func (m *terminalManager) target(ctx context.Context, req terminalRPC, id string) (*net.UDPAddr, error) {
	if id == m.host.ID && id != "" {
		return nil, nil
	}
	_, peers := m.targets(ctx, req)
	if peer := peers[id]; peer != nil {
		return peer, nil
	}
	return nil, fmt.Errorf("target host is offline or unavailable through Alan")
}

func (s *Server) terminalAccess(w http.ResponseWriter, r *http.Request) (*terminalManager, terminalRPC) {
	id := identity.FromContext(r.Context())
	pair, ok := r.Context().Value(nativeSessionContextKey{}).(*issuer.Pair)
	if id == nil || !id.HasRole("admin") || !ok || pair == nil {
		nativeError(w, 403, "host terminals are administrator-only")
		return nil, terminalRPC{}
	}
	if s.terminals == nil {
		nativeError(w, 503, "terminal store unavailable")
		return nil, terminalRPC{}
	}
	return s.terminals, terminalRPC{Owner: id.Subject, Session: pair.SessionID}
}

func (s *Server) ListTerminalsAPI(w http.ResponseWriter, r *http.Request) {
	m, req := s.terminalAccess(w, r)
	if m == nil {
		return
	}
	items, err := m.store.ListTerminals(r.Context(), req.Owner)
	if err != nil {
		nativeError(w, 503, err.Error())
		return
	}
	prefs, err := m.store.GetTerminalPreferences(r.Context(), req.Owner)
	if err != nil {
		nativeError(w, 503, err.Error())
		return
	}
	hosts, _ := m.targets(r.Context(), req)
	httpResponseJSON(w, map[string]any{"sessions": items, "preferences": prefs, "targets": hosts}, 200)
}

func (s *Server) TerminalUsersAPI(w http.ResponseWriter, r *http.Request) {
	m, req := s.terminalAccess(w, r)
	if m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	peer, err := m.target(ctx, req, r.PathValue("target"))
	if err != nil {
		nativeError(w, 503, err.Error())
		return
	}
	req.Op = "users"
	reply, err := m.call(ctx, peer, req)
	if err != nil {
		nativeError(w, 503, err.Error())
		return
	}
	httpResponseJSON(w, reply.Users, 200)
}

func (s *Server) CreateTerminalAPI(w http.ResponseWriter, r *http.Request) {
	m, req := s.terminalAccess(w, r)
	if m == nil {
		return
	}
	var body struct {
		TargetID string `json:"target_id"`
		Username string `json:"username"`
		Title    string `json:"title"`
	}
	if !decodeNativeBody(w, r, &body) {
		return
	}
	if body.TargetID == "" || body.Username == "" || len(body.Username) > 128 || len(body.Title) > 80 {
		nativeError(w, 400, "target, Linux user and title (at most 80 bytes) are required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	hosts, peers := m.targets(ctx, req)
	var target *hostterminal.Host
	for _, h := range hosts {
		if h.ID == body.TargetID {
			target = &h
			break
		}
	}
	if target == nil || !target.Available {
		nativeError(w, 503, "target host is unavailable")
		return
	}
	items, err := m.store.ListTerminals(ctx, req.Owner)
	if err != nil {
		nativeError(w, 503, err.Error())
		return
	}
	if len(items) >= 32 {
		nativeError(w, 409, "limit of 32 saved terminals reached")
		return
	}
	if strings.TrimSpace(body.Title) == "" {
		body.Title = body.Username + "@" + target.Name
	}
	item := service.TerminalSession{ID: ulid.Make().String(), OwnerID: req.Owner, TargetID: target.ID, TargetName: target.Name, Username: body.Username, Title: body.Title, Position: len(items)}
	if err := m.store.CreateTerminal(ctx, item); err != nil {
		nativeError(w, 503, err.Error())
		return
	}
	req.ID, req.Op = item.ID, "start"
	_, err = m.call(ctx, peers[target.ID], req)
	// Retain the row on an ambiguous timeout: a remote shell may already exist.
	if err != nil {
		nativeError(w, 502, "Terminal saved, but startup failed: "+err.Error())
		return
	}
	httpResponseJSON(w, item, 201)
}

func (s *Server) UpdateTerminalAPI(w http.ResponseWriter, r *http.Request) {
	m, req := s.terminalAccess(w, r)
	if m == nil {
		return
	}
	var body struct {
		Title    string `json:"title"`
		Position int    `json:"position"`
	}
	if !decodeNativeBody(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Title) == "" || len(body.Title) > 80 || body.Position < 0 || body.Position > 10000 {
		nativeError(w, 400, "invalid terminal title or position")
		return
	}
	item, err := m.store.GetTerminal(r.Context(), req.Owner, r.PathValue("id"))
	if err != nil {
		nativeError(w, 503, err.Error())
		return
	}
	if item == nil {
		nativeError(w, 404, "terminal not found")
		return
	}
	if err := m.store.UpdateTerminal(r.Context(), req.Owner, item.ID, body.Title, body.Position); err != nil {
		nativeError(w, 503, err.Error())
		return
	}
	w.WriteHeader(204)
}

func (s *Server) TerminalActionAPI(w http.ResponseWriter, r *http.Request) {
	m, req := s.terminalAccess(w, r)
	if m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	item, err := m.store.GetTerminal(ctx, req.Owner, r.PathValue("id"))
	if err != nil {
		nativeError(w, 503, err.Error())
		return
	}
	if item == nil {
		nativeError(w, 404, "terminal not found")
		return
	}
	peer, err := m.target(ctx, req, item.TargetID)
	if err != nil {
		nativeError(w, 503, err.Error())
		return
	}
	req.ID, req.Op = item.ID, "start"
	if r.Method == http.MethodDelete {
		req.Op = "stop"
	}
	if _, err := m.call(ctx, peer, req); err != nil {
		nativeError(w, 502, err.Error())
		return
	}
	w.WriteHeader(204)
}

// validTerminalFont keeps the stored font list to plain CSS family names. The
// value is only ever rendered for its own owner, but restricting it here keeps
// arbitrary text out of a style value.
func validTerminalFont(name string) bool {
	if len(name) > 120 {
		return false
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == ' ', c == '-', c == '_', c == '.', c == ',', c == '\'', c == '"':
		default:
			return false
		}
	}
	return true
}

func (s *Server) TerminalPreferencesAPI(w http.ResponseWriter, r *http.Request) {
	m, req := s.terminalAccess(w, r)
	if m == nil {
		return
	}
	var body service.TerminalPreferences
	if !decodeNativeBodyLimit(w, r, &body, 16384) {
		return
	}
	if len(body.DefaultUsers) > 64 || len(body.ActiveID) > 26 {
		nativeError(w, 400, "invalid terminal preferences")
		return
	}
	for k, v := range body.DefaultUsers {
		if len(k) > 128 || len(v) > 128 {
			nativeError(w, 400, "invalid terminal preference")
			return
		}
	}
	switch body.Appearance {
	case "", "dark", "light", "system":
	default:
		nativeError(w, 400, "terminal appearance must be dark, light or system")
		return
	}
	if !validTerminalFont(body.FontFamily) {
		nativeError(w, 400, "invalid terminal font name")
		return
	}
	if body.FontSize != 0 && (body.FontSize < 10 || body.FontSize > 28) {
		nativeError(w, 400, "terminal font size must be between 10 and 28")
		return
	}
	switch body.KeyBar {
	case "", "auto", "on", "off":
	default:
		nativeError(w, 400, "terminal key bar must be auto, on or off")
		return
	}
	if err := m.store.SetTerminalPreferences(r.Context(), req.Owner, body); err != nil {
		nativeError(w, 503, err.Error())
		return
	}
	w.WriteHeader(204)
}
