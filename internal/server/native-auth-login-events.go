package server

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/rakunlabs/ada/middleware/auth/identity"

	"github.com/rakunlabs/at/internal/service"
)

func (a *nativeAuth) recordLoginEvent(r *http.Request, userID, action string) {
	if userID == "" {
		return
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	// Only the socket peer is authoritative. Forwarded headers are caller input.
	if parsed := net.ParseIP(ip); parsed != nil {
		ip = parsed.String()
	} else {
		ip = ""
	}
	agent := strings.ToValidUTF8(r.UserAgent(), "")
	if len(agent) > 512 {
		agent = strings.ToValidUTF8(agent[:512], "")
	}
	event := service.AuthLoginEvent{UserID: userID, Action: action, SourceIP: ip, UserAgent: agent}
	if action == "login_unlocked" || action == "sessions_revoked" {
		if actor := identity.FromContext(r.Context()); actor != nil {
			event.ActorID = actor.Subject
		}
	}
	slog.Info("account login activity", "action", action, "user_id", userID, "actor_id", event.ActorID, "source_ip", ip)
	if store, ok := a.store.(service.AuthLoginEventStorer); ok {
		// A disconnect after verification must not erase its audit event. Keep the
		// write bounded; storage failures are explicit server errors in the log.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
		defer cancel()
		if err := store.RecordAuthLoginEvent(ctx, event); err != nil {
			slog.Error("persist account login activity failed", "user_id", userID, "action", action, "error", err)
		}
	}
}

func (a *nativeAuth) loginEvents(w http.ResponseWriter, r *http.Request) {
	store, ok := a.store.(service.AuthLoginEventStorer)
	if !ok {
		nativeError(w, 503, "login history unavailable")
		return
	}
	user, err := a.store.GetAuthUserByID(r.Context(), r.PathValue("id"))
	if err != nil {
		nativeError(w, 503, "login history unavailable")
		return
	}
	if user == nil {
		nativeError(w, 404, "user not found")
		return
	}
	events, err := store.ListAuthLoginEvents(r.Context(), user.ID, 50)
	if err != nil {
		nativeError(w, 503, "login history unavailable")
		return
	}
	httpResponseJSON(w, map[string]any{"data": events, "retention_days": 90}, 200)
}
