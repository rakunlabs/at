package blob

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

const (
	// s3Service is the SigV4 service name. Bedrock signs "bedrock"; object
	// storage signs "s3".
	s3Service = "s3"
	// s3Algorithm is the only signature version AT speaks.
	s3Algorithm = "AWS4-HMAC-SHA256"
	// s3ErrorBodyMaxBytes bounds how much of an S3 error document is quoted
	// back to the administrator. The real message ("NoSuchBucket",
	// "SignatureDoesNotMatch", "AccessDenied") is always at the front, and a
	// hostile endpoint must not be able to flood the logs.
	s3ErrorBodyMaxBytes = 4096
	// s3RequestTimeout bounds one request including its body transfer. Media
	// objects are capped at 16 MiB, so a minute is generous even on a slow
	// link, while still failing a black-holed endpoint in bounded time.
	s3RequestTimeout = 60 * time.Second
)

// s3Store talks to any S3-compatible object store over net/http with
// hand-rolled SigV4. Only PutObject, GetObject, DeleteObject and HeadBucket
// are implemented: there is no multipart upload and no listing, because a
// media object is a single in-memory image and the database is the index.
type s3Store struct {
	scheme          string
	host            string
	region          string
	bucket          string
	accessKeyID     string
	secretAccessKey string
	// pathStyle addresses https://host/bucket/key instead of
	// https://bucket.host/key. MinIO, Ceph RGW and R2 require it; AWS
	// supports both.
	pathStyle bool
	client    *http.Client
}

func newS3(s service.MediaS3Settings) (*s3Store, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(s.Endpoint), "/"))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("s3 media storage endpoint must be an absolute http(s) URL")
	}
	if s.Bucket == "" || s.Region == "" || s.AccessKeyID == "" || s.SecretAccessKey == "" {
		return nil, errors.New("s3 media storage requires bucket, region, access key id and secret access key")
	}
	return &s3Store{
		scheme:          u.Scheme,
		host:            u.Host,
		region:          s.Region,
		bucket:          s.Bucket,
		accessKeyID:     s.AccessKeyID,
		secretAccessKey: s.SecretAccessKey,
		pathStyle:       s.UsePathStyle,
		client:          &http.Client{Timeout: s3RequestTimeout},
	}, nil
}

// objectURL builds the request URL for key, or the bucket root when key is
// empty. Both the decoded Path and the S3-canonical RawPath are set, so
// url.URL.EscapedPath returns exactly the bytes that must be signed and sent.
func (s *s3Store) objectURL(key string) *url.URL {
	host, raw := s.host, "/"+key
	if s.pathStyle {
		raw = "/" + s.bucket
		if key != "" {
			raw += "/" + key
		}
	} else {
		host = s.bucket + "." + s.host
	}
	return &url.URL{Scheme: s.scheme, Host: host, Path: raw, RawPath: s3EscapePath(raw)}
}

// s3EscapePath percent-encodes a path the way S3 canonicalisation requires:
// every byte outside the unreserved set is encoded, and '/' is left alone.
// This is deliberately not url.PathEscape, which leaves sub-delimiters such as
// '$', '+', '&' and ':' untouched and would produce a canonical URI that does
// not match the one S3 reconstructs, yielding SignatureDoesNotMatch.
func s3EscapePath(p string) string {
	var b strings.Builder
	b.Grow(len(p))
	for i := 0; i < len(p); i++ {
		c := p[i]
		switch {
		case c == '/' ||
			(c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func (s *s3Store) Put(ctx context.Context, key, contentType string, data []byte) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	resp, err := s.do(ctx, http.MethodPut, s.objectURL(key), data, contentType)
	if err != nil {
		return err
	}
	return s3Drain(resp)
}

func (s *s3Store) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	if err := ValidateKey(key); err != nil {
		return nil, "", err
	}
	resp, err := s.do(ctx, http.MethodGet, s.objectURL(key), nil, "")
	if err != nil {
		return nil, "", err
	}
	return resp.Body, resp.Header.Get("Content-Type"), nil
}

func (s *s3Store) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	resp, err := s.do(ctx, http.MethodDelete, s.objectURL(key), nil, "")
	if err != nil {
		// A missing object is not a failure: the database row is removed in
		// the same flow and a retry must converge.
		var status *s3StatusError
		if errors.As(err, &status) && status.code == http.StatusNotFound {
			return nil
		}
		return err
	}
	return s3Drain(resp)
}

// Check issues HeadBucket. It proves the endpoint resolves, the credentials
// sign correctly and the bucket exists, without creating an object.
func (s *s3Store) Check(ctx context.Context) error {
	resp, err := s.do(ctx, http.MethodHead, s.objectURL(""), nil, "")
	if err != nil {
		return err
	}
	return s3Drain(resp)
}

func s3Drain(resp *http.Response) error {
	defer resp.Body.Close()
	_, err := io.Copy(io.Discard, io.LimitReader(resp.Body, s3ErrorBodyMaxBytes))
	if err != nil {
		return fmt.Errorf("drain s3 response: %w", err)
	}
	return nil
}

// s3StatusError carries the upstream status so callers can special-case it
// (Delete tolerates 404) while still reporting the real S3 message.
type s3StatusError struct {
	code    int
	message string
}

func (e *s3StatusError) Error() string { return e.message }

// do signs and performs one request. Non-2xx responses are returned as Go
// errors that quote the S3 error document: an administrator debugging a
// misconfigured bucket needs the upstream message, not "request failed".
func (s *s3Store) do(ctx context.Context, method string, u *url.URL, body []byte, contentType string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("build s3 request: %w", err)
	}
	req.ContentLength = int64(len(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// The payload is already in memory, so the real hash is signed.
	// UNSIGNED-PAYLOAD exists only for streams whose bytes are not known in
	// advance, and it weakens the signature.
	sum := sha256.Sum256(body)
	s.sign(req, hex.EncodeToString(sum[:]), time.Now().UTC())
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3 %s %s: %w", method, u.EscapedPath(), err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		defer resp.Body.Close()
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, s3ErrorBodyMaxBytes))
		message := fmt.Sprintf("s3 %s %s: %s", method, u.EscapedPath(), resp.Status)
		if trimmed := strings.TrimSpace(string(detail)); trimmed != "" {
			message += ": " + trimmed
		}
		return nil, &s3StatusError{code: resp.StatusCode, message: message}
	}
	return resp, nil
}

// sign attaches SigV4 headers for the s3 service. now and payloadHash are
// explicit parameters so the signature is reproducible in tests against the
// published AWS test vectors.
func (s *s3Store) sign(req *http.Request, payloadHash string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	dateStamp := now.UTC().Format("20060102")
	// x-amz-content-sha256 is mandatory on S3 (unlike Bedrock, where it is
	// merely conventional) and is part of the signed header set.
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	canonicalHeaders, signedHeaders := s3CanonicalHeaders(req)
	canonicalRequest := strings.Join([]string{
		req.Method,
		req.URL.EscapedPath(),
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{dateStamp, s.region, s3Service, "aws4_request"}, "/")
	canonicalHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{s3Algorithm, amzDate, scope, hex.EncodeToString(canonicalHash[:])}, "\n")

	key := s3HMAC([]byte("AWS4"+s.secretAccessKey), dateStamp)
	key = s3HMAC(key, s.region)
	key = s3HMAC(key, s3Service)
	key = s3HMAC(key, "aws4_request")
	signature := hex.EncodeToString(s3HMAC(key, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf("%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s3Algorithm, s.accessKeyID, scope, signedHeaders, signature))
}

// s3CanonicalHeaders signs the Host header plus every header present on the
// request except the ones the transport owns. Signing everything that is sent
// is always valid and keeps the signature honest when a caller adds a header.
func s3CanonicalHeaders(req *http.Request) (string, string) {
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	values := map[string]string{"host": host}
	for name, list := range req.Header {
		lower := strings.ToLower(name)
		switch lower {
		// Authorization is the output; User-Agent and Content-Length may be
		// rewritten by the transport after signing.
		case "authorization", "user-agent", "content-length":
			continue
		}
		parts := make([]string, 0, len(list))
		for _, v := range list {
			parts = append(parts, strings.Join(strings.Fields(v), " "))
		}
		values[lower] = strings.Join(parts, ",")
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonical strings.Builder
	for _, name := range names {
		canonical.WriteString(name)
		canonical.WriteString(":")
		canonical.WriteString(values[name])
		canonical.WriteString("\n")
	}
	return canonical.String(), strings.Join(names, ";")
}

func s3HMAC(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}
