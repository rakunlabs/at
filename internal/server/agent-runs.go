package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Durable agent runs are the shared runtime of every surface whose turn runs
// on the server and outlives the page: Developer Spaces sessions and Chats
// background runs. Each surface keeps its own record, admission and stale
// policy; this file owns what is identical between them — the event stream,
// receipt renewal and cancellation, and replay-scoped stream lookup.

// runEventStream writes one run's server-sent events. Writes are serialized
// because heartbeats and subagent progress report from other goroutines.
type runEventStream struct {
	mu      sync.Mutex
	w       http.ResponseWriter
	flusher http.Flusher
}

// newRunEventStream commits the SSE response. After this, failures can only be
// reported in-band.
func newRunEventStream(w http.ResponseWriter) (*runEventStream, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpResponse(w, "streaming not supported", http.StatusInternalServerError)
		return nil, false
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	return &runEventStream{w: w, flusher: flusher}, true
}

func (s *runEventStream) send(event map[string]any) {
	if s == nil {
		return
	}
	data, _ := json.Marshal(event)
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = fmt.Fprintf(s.w, "data: %s\n\n", data)
	s.flusher.Flush()
}

// fail reports a terminal error on the open stream.
func (s *runEventStream) fail(message string) {
	s.send(map[string]any{"type": "error", "error": message})
}

// keepAlive writes a comment every 15s so proxies keep a quiet run open.
func (s *runEventStream) keepAlive(ctx context.Context) func() {
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				_, _ = fmt.Fprint(s.w, ": ping\n\n")
				s.flusher.Flush()
				s.mu.Unlock()
			}
		}
	}()
	return func() { close(done); <-stopped }
}

// runReceipt is a surface's durable exclusion record for one run.
type runReceipt struct {
	// Renew extends the receipt. Any error cancels the run, so a surface
	// returns nil for errors it treats as transient.
	Renew func(context.Context) error
	// Check, when set, is polled between renewals so a cancellation requested
	// on another replica stops the run within one poll.
	Check func(context.Context) error
	// Release clears the receipt. It runs on an uncancelled context.
	Release func(context.Context) error
	Label   string
}

const (
	runReceiptPoll  = 5 * time.Second
	runReceiptRenew = 15 * time.Second
)

// errRunReceiptLost is the cancellation cause when renewal or a check fails.
var errRunReceiptLost = fmt.Errorf("run ownership lost or cancelled")

// holdRunReceipt keeps a held receipt alive until release is called. The
// returned context is cancelled with errRunReceiptLost when ownership is lost.
// release is idempotent; call it before reporting the terminal event so the
// next run is never refused by the one that just ended.
func holdRunReceipt(ctx context.Context, receipt runReceipt) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		interval := runReceiptRenew
		if receipt.Check != nil {
			interval = runReceiptPoll
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var since time.Duration
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				since += interval
				step := receipt.Check
				if since >= runReceiptRenew || step == nil {
					since, step = 0, receipt.Renew
				}
				check, stop := context.WithTimeout(ctx, 5*time.Second)
				err := step(check)
				stop()
				// The surface decides which errors mean lost ownership; a check
				// cut short by the run ending is never one.
				if err != nil && ctx.Err() == nil {
					cancel(errRunReceiptLost)
					return
				}
			}
		}
	}()
	release := sync.OnceFunc(func() {
		close(done)
		<-stopped
		cancel(nil)
		finish, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stop()
		if err := receipt.Release(finish); err != nil {
			slog.Error("release run receipt failed", "run", receipt.Label, "error", err)
		}
	})
	return ctx, release
}

// replayStreamMatch selects the live replay streams of one run scope.
type replayStreamMatch struct {
	kind                      replayKind
	session, owner, workspace string
}

func (m replayStreamMatch) matches(stream *chatStream) bool {
	return stream.kind == m.kind && stream.session == m.session && stream.owner == m.owner && stream.workspace == m.workspace
}

// activeReplayStream returns the ID of the scope's unfinished replay on this
// replica, or "".
func (s *Server) activeReplayStream(m replayStreamMatch) string {
	s.chatStreams.mu.Lock()
	defer s.chatStreams.mu.Unlock()
	for key, stream := range s.chatStreams.streams {
		stream.mu.Lock()
		live := m.matches(stream) && !stream.done
		stream.mu.Unlock()
		if live {
			return key
		}
	}
	return ""
}

// cancelReplayStreams stops the scope's runs executing on this replica.
func (s *Server) cancelReplayStreams(m replayStreamMatch) {
	s.chatStreams.mu.Lock()
	defer s.chatStreams.mu.Unlock()
	for _, stream := range s.chatStreams.streams {
		if m.matches(stream) {
			stream.cancel()
		}
	}
}

// replayRunStream serves GET .../streams/{stream}?offset= for one scope.
// Unknown or foreign IDs answer 404 identically; replay never restarts work.
func (s *Server) replayRunStream(w http.ResponseWriter, r *http.Request, m replayStreamMatch) {
	s.chatStreams.mu.Lock()
	stream := s.chatStreams.streams[r.PathValue("stream")]
	s.chatStreams.mu.Unlock()
	if stream == nil || !m.matches(stream) {
		httpResponse(w, "live run unavailable on this server; check saved history, do not resend automatically", http.StatusNotFound)
		return
	}
	stream.mu.Lock()
	expired := stream.done && time.Now().After(stream.expires)
	stream.mu.Unlock()
	if expired {
		httpResponse(w, "live run replay expired; check saved history", http.StatusGone)
		return
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || offset < 0 {
		httpResponse(w, "invalid stream offset", http.StatusBadRequest)
		return
	}
	s.replayChatStream(w, r, stream, offset)
}
