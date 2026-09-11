package blob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

func newS3Store(t *testing.T, endpoint string, pathStyle bool) *s3Store {
	t.Helper()
	store, err := newS3(service.MediaS3Settings{
		Endpoint:        endpoint,
		Region:          "us-east-1",
		Bucket:          "media",
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		UsePathStyle:    pathStyle,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// TestS3SignatureFixedVector pins the signature for a fixed date, key, payload
// and header set so a refactor of the canonicalisation cannot silently break
// signing (the failure mode is an opaque upstream SignatureDoesNotMatch).
//
// The inputs are AWS's own published "PUT Object, single chunk" example:
// credentials AKIAIOSFODNN7EXAMPLE / wJalrX..., bucket examplebucket, key
// "test$file.text", payload "Welcome to Amazon S3.", 2013-05-24T00:00:00Z,
// us-east-1. The expected canonical-request digest and signature below were
// cross-checked against an independent SigV4 implementation (botocore's
// SigV4Auth) signing byte-identical inputs; the payload digest
// 44ce7dd6... and the canonical-request digest 9e0e90d9... are the values
// published in the AWS documentation for this example.
func TestS3SignatureFixedVector(t *testing.T) {
	const (
		wantPayloadHash = "44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072"
		wantAuth        = "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
			"SignedHeaders=date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class, " +
			"Signature=7c0f3caf24a16d5948905b8ebf67d29fb415e93fddaed9ca6aeb5ac2348cfee4"
	)
	store := newS3Store(t, "https://s3.amazonaws.com", false)
	store.bucket = "examplebucket"

	payload := []byte("Welcome to Amazon S3.")
	sum := sha256.Sum256(payload)
	payloadHash := hex.EncodeToString(sum[:])
	if payloadHash != wantPayloadHash {
		t.Fatalf("payload hash %s, want %s", payloadHash, wantPayloadHash)
	}

	u := store.objectURL("test$file.text")
	if u.Host != "examplebucket.s3.amazonaws.com" || u.EscapedPath() != "/test%24file.text" {
		t.Fatalf("url %s %s", u.Host, u.EscapedPath())
	}
	req, err := http.NewRequest(http.MethodPut, u.String(), bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Date", "Fri, 24 May 2013 00:00:00 GMT")
	req.Header.Set("X-Amz-Storage-Class", "REDUCED_REDUNDANCY")

	store.sign(req, payloadHash, time.Date(2013, time.May, 24, 0, 0, 0, 0, time.UTC))

	if got := req.Header.Get("Authorization"); got != wantAuth {
		t.Fatalf("authorization:\n got %s\nwant %s", got, wantAuth)
	}
	if got := req.Header.Get("X-Amz-Content-Sha256"); got != wantPayloadHash {
		t.Fatalf("x-amz-content-sha256 %s", got)
	}
	if got := req.Header.Get("X-Amz-Date"); got != "20130524T000000Z" {
		t.Fatalf("x-amz-date %s", got)
	}
}

// S3 canonicalisation encodes every byte outside the unreserved set and leaves
// '/' alone. url.PathEscape would leave sub-delimiters such as '$', '+', '&'
// and ':' untouched, which is the classic source of SignatureDoesNotMatch.
func TestS3EscapePath(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"unreserved", "/a-Z_0.9~/b", "/a-Z_0.9~/b"},
		{"space", "/owner/a b.png", "/owner/a%20b.png"},
		{"plus", "/owner/a+b.png", "/owner/a%2Bb.png"},
		{"dollar", "/test$file.text", "/test%24file.text"},
		{"ampersand and equals", "/a&b=c", "/a%26b%3Dc"},
		{"colon", "/a:b", "/a%3Ab"},
		{"percent", "/a%20b", "/a%2520b"},
		{"unicode", "/owner/é.png", "/owner/%C3%A9.png"},
		{"emoji", "/owner/🙂.png", "/owner/%F0%9F%99%82.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s3EscapePath(tt.in); got != tt.want {
				t.Fatalf("s3EscapePath(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestS3ObjectURL(t *testing.T) {
	tests := []struct {
		name      string
		pathStyle bool
		key       string
		wantHost  string
		wantPath  string
	}{
		{"virtual host object", false, "owner/a b.png", "media.s3.example.test", "/owner/a%20b.png"},
		{"virtual host bucket root", false, "", "media.s3.example.test", "/"},
		{"path style object", true, "owner/a b.png", "s3.example.test", "/media/owner/a%20b.png"},
		{"path style bucket root", true, "", "s3.example.test", "/media"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newS3Store(t, "https://s3.example.test", tt.pathStyle)
			u := store.objectURL(tt.key)
			if u.Host != tt.wantHost || u.EscapedPath() != tt.wantPath {
				t.Fatalf("host %q path %q", u.Host, u.EscapedPath())
			}
			// The URL must survive a String/Parse round trip unchanged,
			// because that is how the request is built.
			parsed, err := url.Parse(u.String())
			if err != nil || parsed.EscapedPath() != tt.wantPath || parsed.Host != tt.wantHost {
				t.Fatalf("round trip %q %v", u.String(), err)
			}
		})
	}
}

// s3Recorder captures what the store actually put on the wire.
type s3Recorder struct {
	method      string
	requestURI  string
	host        string
	auth        string
	sha         string
	amzDate     string
	contentType string
	body        []byte
}

func newS3TestServer(t *testing.T, rec *s3Recorder, status int, responseBody, responseType string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.method, rec.requestURI, rec.host = r.Method, r.RequestURI, r.Host
		rec.auth, rec.sha, rec.amzDate = r.Header.Get("Authorization"), r.Header.Get("X-Amz-Content-Sha256"), r.Header.Get("X-Amz-Date")
		rec.contentType, rec.body = r.Header.Get("Content-Type"), body
		if responseType != "" {
			w.Header().Set("Content-Type", responseType)
		}
		w.WriteHeader(status)
		w.Write([]byte(responseBody)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestS3PathStyleRoundTrip(t *testing.T) {
	rec := &s3Recorder{}
	srv := newS3TestServer(t, rec, http.StatusOK, "", "")
	store := newS3Store(t, srv.URL, true)
	ctx := t.Context()

	// A key with a space, a plus and a non-ASCII rune exercises the exact
	// canonicalisation the signature depends on.
	const key = "owner-1/a b+ü.png"
	payload := []byte("png-bytes")
	if err := store.Put(ctx, key, "image/png", payload); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	if rec.method != http.MethodPut || rec.requestURI != "/media/owner-1/a%20b%2B%C3%BC.png" {
		t.Fatalf("request line %s %s", rec.method, rec.requestURI)
	}
	if rec.sha != hex.EncodeToString(sum[:]) {
		t.Fatalf("x-amz-content-sha256 %s", rec.sha)
	}
	if rec.contentType != "image/png" || !bytes.Equal(rec.body, payload) {
		t.Fatalf("content type %q body %q", rec.contentType, rec.body)
	}
	if !strings.HasPrefix(rec.auth, "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/") ||
		!strings.Contains(rec.auth, "/us-east-1/s3/aws4_request") ||
		!strings.Contains(rec.auth, "SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date,") {
		t.Fatalf("authorization %s", rec.auth)
	}
	if len(rec.auth) < 64 || !strings.Contains(rec.auth, "Signature=") {
		t.Fatalf("authorization %s", rec.auth)
	}
	if rec.amzDate == "" || rec.host != srv.Listener.Addr().String() {
		t.Fatalf("date %q host %q", rec.amzDate, rec.host)
	}

	// The empty payload of a GET is still hashed, never UNSIGNED-PAYLOAD.
	empty := sha256.Sum256(nil)
	reader, contentType, err := store.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	reader.Close()
	if rec.method != http.MethodGet || rec.sha != hex.EncodeToString(empty[:]) || contentType != "" {
		t.Fatalf("get %s %s %q", rec.method, rec.sha, contentType)
	}
	if err := store.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if rec.method != http.MethodDelete {
		t.Fatalf("delete method %s", rec.method)
	}
	if err := store.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.method != http.MethodHead || rec.requestURI != "/media" {
		t.Fatalf("check %s %s", rec.method, rec.requestURI)
	}
}

// Virtual-host style is exercised through a transport that dials the test
// server regardless of the bucket hostname, so the request line and Host
// header are asserted as they would reach a real endpoint.
func TestS3VirtualHostStyle(t *testing.T) {
	rec := &s3Recorder{}
	srv := newS3TestServer(t, rec, http.StatusOK, "image-bytes", "image/jpeg")
	store := newS3Store(t, "https://s3.example.test", false)
	store.client = &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
		},
	}}
	// The test server speaks cleartext HTTP; only the host matters here.
	store.scheme = "http"

	reader, contentType, err := store.Get(t.Context(), "owner-1/a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(reader)
	reader.Close()
	if string(data) != "image-bytes" || contentType != "image/jpeg" {
		t.Fatalf("body %q type %q", data, contentType)
	}
	if rec.requestURI != "/owner-1/a.jpg" || rec.host != "media.s3.example.test" {
		t.Fatalf("request %s host %s", rec.requestURI, rec.host)
	}
	if err := store.Check(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rec.requestURI != "/" || rec.host != "media.s3.example.test" {
		t.Fatalf("check request %s host %s", rec.requestURI, rec.host)
	}
}

// An administrator debugging a misconfigured bucket needs the upstream message.
func TestS3ErrorsSurfaceUpstreamBody(t *testing.T) {
	rec := &s3Recorder{}
	srv := newS3TestServer(t, rec, http.StatusForbidden,
		`<?xml version="1.0"?><Error><Code>SignatureDoesNotMatch</Code><Message>bad signature</Message></Error>`, "application/xml")
	store := newS3Store(t, srv.URL, true)

	err := store.Put(t.Context(), "owner-1/a.png", "image/png", []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "SignatureDoesNotMatch") {
		t.Fatalf("error %v", err)
	}
	if _, _, err := store.Get(t.Context(), "owner-1/a.png"); err == nil || !strings.Contains(err.Error(), "bad signature") {
		t.Fatalf("get error %v", err)
	}
	if err := store.Check(t.Context()); err == nil || !strings.Contains(err.Error(), "s3 HEAD /media") {
		t.Fatalf("check error %v", err)
	}
}

// A truncated error document must not be able to flood the caller.
func TestS3ErrorBodyIsBounded(t *testing.T) {
	rec := &s3Recorder{}
	srv := newS3TestServer(t, rec, http.StatusInternalServerError, strings.Repeat("x", 32<<10), "text/plain")
	store := newS3Store(t, srv.URL, true)
	err := store.Put(t.Context(), "owner-1/a.png", "image/png", []byte("x"))
	if err == nil || len(err.Error()) > s3ErrorBodyMaxBytes+256 {
		t.Fatalf("error length %d", len(err.Error()))
	}
}

// Delete converges: the row is gone either way, so a missing object is not an
// error, while other failures still are.
func TestS3DeleteToleratesMissingObject(t *testing.T) {
	rec := &s3Recorder{}
	srv := newS3TestServer(t, rec, http.StatusNotFound, `<Error><Code>NoSuchKey</Code></Error>`, "application/xml")
	store := newS3Store(t, srv.URL, true)
	if err := store.Delete(t.Context(), "owner-1/a.png"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Get(t.Context(), "owner-1/a.png"); err == nil {
		t.Fatal("expected a missing object to fail on read")
	}
}

func TestS3RejectsHostileKeys(t *testing.T) {
	rec := &s3Recorder{}
	srv := newS3TestServer(t, rec, http.StatusOK, "", "")
	store := newS3Store(t, srv.URL, true)
	for _, key := range []string{"", "/absolute.png", "../escape.png", "a/../../b.png", "a\\b.png"} {
		if err := store.Put(t.Context(), key, "image/png", []byte("x")); err == nil {
			t.Fatalf("Put(%q) succeeded", key)
		}
		if err := store.Delete(t.Context(), key); err == nil {
			t.Fatalf("Delete(%q) succeeded", key)
		}
		if _, _, err := store.Get(t.Context(), key); err == nil {
			t.Fatalf("Get(%q) succeeded", key)
		}
	}
	if rec.method != "" {
		t.Fatalf("a hostile key reached the network: %s %s", rec.method, rec.requestURI)
	}
}
