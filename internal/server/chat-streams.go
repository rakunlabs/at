package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// Replay is replica-local and bounded. Unknown IDs are never relaunched: an
// outage/restart may lose replay, but cannot repeat an agent's tool effects.
const chatStreamMaxBytes = 8 << 20
const chatStreamTTL = 10 * time.Minute

type chatStreamRegistry struct {
	mu      sync.Mutex
	streams map[string]*chatStream
}

type chatStream struct {
	mu                        sync.Mutex
	header                    http.Header
	status                    int
	data                      []byte
	notify                    chan struct{}
	done                      bool
	expires                   time.Time
	expireTimer               *time.Timer
	owner, workspace, session string
	developer                 bool
	cancel                    context.CancelFunc
}

func (stream *chatStream) Header() http.Header { return stream.header }
func (stream *chatStream) signal()             { close(stream.notify); stream.notify = make(chan struct{}) }
func (stream *chatStream) WriteHeader(status int) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.status == 0 {
		stream.status = status
		stream.signal()
	}
}
func (stream *chatStream) Write(p []byte) (int, error) {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.status == 0 {
		stream.status = http.StatusOK
	}
	if len(stream.data)+len(p) > chatStreamMaxBytes {
		stream.cancel()
		return 0, fmt.Errorf("stream replay exceeded 8 MiB; check saved history")
	}
	stream.data = append(stream.data, p...)
	stream.signal()
	return len(p), nil
}
func (stream *chatStream) Flush() {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.status == 0 {
		stream.status = http.StatusOK
	}
	stream.signal()
}

func (s *Server) startChatStream(w http.ResponseWriter, r *http.Request, handler http.HandlerFunc) {
	s.startReplayStream(w, r, handler, false)
}

func (s *Server) startReplayStream(w http.ResponseWriter, r *http.Request, handler http.HandlerFunc, developer bool) {
	principal, ok := service.AccessPrincipalFromContext(r.Context())
	id := r.Header.Get("X-AT-Stream-ID")
	if !ok || principal.UserID == "" {
		nativeError(w, http.StatusForbidden, "stream requires an account")
		return
	}
	if len(id) < 16 || len(id) > 128 || strings.ContainsAny(id, "/\\ \t\r\n") {
		nativeError(w, http.StatusBadRequest, "invalid stream id")
		return
	}
	// Take ownership of the body before the request handler returns. Admission
	// and the original execution identity survive detachment, not HTTP deadlines.
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20+1))
	if err != nil {
		nativeError(w, http.StatusBadRequest, "cannot read stream request")
		return
	}
	if len(body) > 16<<20 {
		nativeError(w, http.StatusRequestEntityTooLarge, "stream request exceeds 16 MiB")
		return
	}
	s.chatStreams.mu.Lock()
	if s.chatStreams.streams == nil {
		s.chatStreams.streams = make(map[string]*chatStream)
	}
	now := time.Now()
	for key, stream := range s.chatStreams.streams {
		stream.mu.Lock()
		expired := stream.done && now.After(stream.expires)
		if expired && stream.expireTimer != nil {
			stream.expireTimer.Stop()
		}
		stream.mu.Unlock()
		if expired {
			delete(s.chatStreams.streams, key)
		}
	}
	if _, exists := s.chatStreams.streams[id]; exists {
		s.chatStreams.mu.Unlock()
		nativeError(w, http.StatusConflict, "stream already started; reconnect with GET")
		return
	}
	if len(s.chatStreams.streams) >= 32 {
		// Prefer eviction of the oldest completed replay over refusing the
		// next tool-loop completion. Active execution is never evicted/restarted.
		oldestID := ""
		var oldest time.Time
		for key, retained := range s.chatStreams.streams {
			retained.mu.Lock()
			if retained.done && (oldestID == "" || retained.expires.Before(oldest)) {
				oldestID, oldest = key, retained.expires
			}
			retained.mu.Unlock()
		}
		if oldestID == "" {
			s.chatStreams.mu.Unlock()
			nativeError(w, http.StatusServiceUnavailable, "stream replay capacity reached; retry later")
			return
		}
		evicted := s.chatStreams.streams[oldestID]
		evicted.mu.Lock()
		if evicted.expireTimer != nil {
			evicted.expireTimer.Stop()
		}
		evicted.mu.Unlock()
		delete(s.chatStreams.streams, oldestID)
	}
	session := r.PathValue("id")
	if session != "" {
		for _, active := range s.chatStreams.streams {
			active.mu.Lock()
			busy := !active.done && active.developer == developer && active.session == session && active.workspace == principal.WorkspaceID
			active.mu.Unlock()
			if busy {
				s.chatStreams.mu.Unlock()
				nativeError(w, http.StatusConflict, "session already has an active turn")
				return
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Minute)
	stopShutdown := func() bool { return false }
	if s.ctx != nil {
		stopShutdown = context.AfterFunc(s.ctx, cancel)
	}
	stream := &chatStream{header: make(http.Header), notify: make(chan struct{}), owner: principal.UserID, workspace: principal.WorkspaceID, session: session, cancel: cancel,
		developer: developer}
	s.chatStreams.streams[id] = stream
	s.chatStreams.mu.Unlock()
	request := r.Clone(ctx)
	request.Header.Del("X-AT-Stream-ID")
	request.Body = io.NopCloser(bytes.NewReader(body))
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("chat stream handler panicked", "panic", recovered)
				stream.Write([]byte("event: error\ndata: {\"error\":\"Stream execution failed; check saved history\"}\n\n"))
			}
			cancel()
			stopShutdown()
			stream.mu.Lock()
			if stream.status == http.StatusOK && strings.HasPrefix(stream.header.Get("Content-Type"), "text/event-stream") {
				stream.data = append(stream.data, []byte(": at-stream-complete\n\n")...)
			}
			stream.done = true
			stream.expires = time.Now().Add(chatStreamTTL)
			stream.expireTimer = time.AfterFunc(chatStreamTTL, func() {
				s.chatStreams.mu.Lock()
				defer s.chatStreams.mu.Unlock()
				if s.chatStreams.streams[id] == stream {
					delete(s.chatStreams.streams, id)
				}
			})
			stream.signal()
			stream.mu.Unlock()
		}()
		handler(stream, request)
	}()
	s.replayChatStream(w, r, stream, 0)
}

func (s *Server) ChatStreamAPI(w http.ResponseWriter, r *http.Request) {
	p, ok := service.AccessPrincipalFromContext(r.Context())
	s.chatStreams.mu.Lock()
	stream := s.chatStreams.streams[r.PathValue("stream")]
	s.chatStreams.mu.Unlock()
	if !ok || stream == nil || stream.developer || stream.owner != p.UserID || stream.workspace != p.WorkspaceID || stream.session != r.PathValue("id") {
		nativeError(w, http.StatusNotFound, "stream replay unavailable on this server; check saved history, do not resend automatically")
		return
	}
	stream.mu.Lock()
	expired := stream.done && time.Now().After(stream.expires)
	stream.mu.Unlock()
	if expired {
		nativeError(w, http.StatusGone, "stream replay expired; check saved history")
		return
	}
	if stream.session != "" {
		if existing, status, msg := s.chatSessionForRequest(r, stream.session); existing == nil {
			httpResponse(w, msg, status)
			return
		}
	}
	if r.Method == http.MethodDelete {
		stream.cancel()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || offset < 0 {
		nativeError(w, http.StatusBadRequest, "invalid stream offset")
		return
	}
	s.replayChatStream(w, r, stream, offset)
}

func (s *Server) replayChatStream(w http.ResponseWriter, r *http.Request, stream *chatStream, offset int) {
	committed := false
	for {
		stream.mu.Lock()
		if offset > len(stream.data) {
			stream.mu.Unlock()
			nativeError(w, http.StatusConflict, "stream offset exceeds recorded output")
			return
		}
		status, done, changed := stream.status, stream.done, stream.notify
		if status == 0 && !done {
			stream.mu.Unlock()
			select {
			case <-r.Context().Done():
				return
			case <-changed:
				continue
			}
		}
		if !committed {
			for key, values := range stream.header {
				w.Header()[key] = append([]string(nil), values...)
			}
			w.Header().Set("X-AT-Stream-Replay", "1")
			w.Header().Set("X-AT-Stream-Complete", strconv.FormatBool(done))
			if done {
				w.Header().Set("X-AT-Stream-Length", strconv.Itoa(len(stream.data)))
			}
			w.Header().Set("Cache-Control", "no-store")
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			committed = true
		}
		data := append([]byte(nil), stream.data[offset:]...)
		stream.mu.Unlock()
		if len(data) > 0 {
			if _, err := w.Write(data); err != nil {
				return
			}
			offset += len(data)
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if done {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-changed:
		}
	}
}
