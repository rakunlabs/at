package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// stripeSignatureTolerance bounds how old a Stripe signature timestamp may be,
// matching Stripe's own libraries; it is what makes a captured request
// unreplayable after five minutes.
const stripeSignatureTolerance = 5 * time.Minute

var errWebhookSignature = errors.New("webhook signature mismatch")

// verifyWebhookSignature checks an HMAC-SHA256 signature over the raw body.
// The comparison is constant-time; every failure is reported the same way to
// the caller, the detail only reaches the delivery log.
func verifyWebhookSignature(sig service.WebhookSignature, h http.Header, body []byte, now time.Time) error {
	if sig.Secret == "" {
		return fmt.Errorf("%w: signing secret unavailable", errWebhookSignature)
	}
	header := h.Get(sig.Header)
	if header == "" {
		return fmt.Errorf("%w: missing %s header", errWebhookSignature, sig.Header)
	}
	if sig.Scheme == service.WebhookSignatureStripe {
		return verifyStripeSignature(sig.Secret, header, body, now)
	}
	got := strings.TrimSpace(header)
	if sig.Prefix != "" {
		if !strings.HasPrefix(got, sig.Prefix) {
			return fmt.Errorf("%w: expected %q prefix", errWebhookSignature, sig.Prefix)
		}
		got = strings.TrimPrefix(got, sig.Prefix)
	}
	mac := hmac.New(sha256.New, []byte(sig.Secret))
	mac.Write(body)
	sum := mac.Sum(nil)
	var want string
	if sig.Encoding == "base64" {
		want = base64.StdEncoding.EncodeToString(sum)
	} else {
		want = hex.EncodeToString(sum)
		got = strings.ToLower(got)
	}
	if !hmac.Equal([]byte(got), []byte(want)) {
		return errWebhookSignature
	}
	return nil
}

// verifyStripeSignature implements Stripe's `t=<unix>,v1=<hex>[,v1=...]`
// scheme: the signed payload is "<t>.<body>" and any v1 entry may match
// (Stripe sends several while a secret is being rolled).
func verifyStripeSignature(secret, header string, body []byte, now time.Time) error {
	var ts string
	var candidates []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = v
		case "v1":
			candidates = append(candidates, strings.ToLower(v))
		}
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || len(candidates) == 0 {
		return fmt.Errorf("%w: malformed Stripe-Signature", errWebhookSignature)
	}
	if d := now.Sub(time.Unix(unix, 0)); d > stripeSignatureTolerance || d < -stripeSignatureTolerance {
		return fmt.Errorf("%w: timestamp outside tolerance", errWebhookSignature)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte("."))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	for _, c := range candidates {
		if hmac.Equal([]byte(c), []byte(want)) {
			return nil
		}
	}
	return errWebhookSignature
}

// webhookStatusWriter remembers the status and a short prefix of the body so
// the delivery log can say why a request was refused.
type webhookStatusWriter struct {
	http.ResponseWriter
	status int
	body   []byte
}

func (w *webhookStatusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *webhookStatusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if room := 512 - len(w.body); room > 0 {
		w.body = append(w.body, b[:min(room, len(b))]...)
	}
	return w.ResponseWriter.Write(b)
}

func (w *webhookStatusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *webhookStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *webhookStatusWriter) statusCode() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *webhookStatusWriter) message() string {
	var v struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(w.body, &v) == nil && v.Message != "" {
		return v.Message
	}
	return strings.TrimSpace(string(w.body))
}

func (s *Server) webhookClientIP(r *http.Request) string {
	return s.clientIPs.ClientIP(r)
}

// recordWebhookDelivery is fire-and-forget: a delivery log failure must never
// change what the caller was told.
func (s *Server) recordWebhookDelivery(d service.WebhookDelivery) {
	store, ok := s.store.(service.WebhookServerStorer)
	if !ok || d.TriggerID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := store.RecordWebhookDelivery(ctx, d); err != nil {
			slog.Warn("webhook: record delivery failed", "trigger_id", d.TriggerID, "error", err)
		}
	}()
}
