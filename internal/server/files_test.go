package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsWithinAllowedRoot pins the allow-list semantics — exact root match,
// strict descendant, dynamic /tmp/at-org-* prefix, and the
// /tmp/at-tasks-evil bypass we explicitly defend against.
func TestIsWithinAllowedRoot(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"exact root match", "/tmp/at-tasks", true},
		{"descendant of root", "/tmp/at-tasks/01ABC/scene.png", true},
		{"another root", "/tmp/at-sandbox", true},
		{"audio root", "/tmp/at-audio/123/voice.mp3", true},
		{"git cache", "/tmp/at-git-cache/repo/file.go", true},
		{"dynamic org prefix", "/tmp/at-org-abc123", true},
		{"dynamic org descendant", "/tmp/at-org-abc123/sub/file.txt", true},
		// Negative cases.
		{"outside /tmp", "/etc/passwd", false},
		{"home dir", "/home/ray/.aws/credentials", false},
		{"sibling that prefix-collides", "/tmp/at-tasks-evil/x", false},
		{"another collision", "/tmp/at-sandbox-other", false},
		{"prefix without suffix", "/tmp/at-org-", false},
		{"plain /tmp", "/tmp", false},
		{"plain /tmp/something", "/tmp/something", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isWithinAllowedRoot(tt.path)
			if got != tt.want {
				t.Errorf("isWithinAllowedRoot(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// browseRequest is a tiny helper for hitting FileBrowseAPI in tests.
func browseRequest(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/v1/files/browse?path=" + path
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	s.FileBrowseAPI(rec, req)
	return rec
}

// TestFileBrowseAPIRejectsTraversal — paths outside the allow-list must
// return 403 even when they exist (the daemon is unprivileged but can read
// /etc/passwd).
func TestFileBrowseAPIRejectsTraversal(t *testing.T) {
	s := &Server{}
	for _, raw := range []string{
		"/etc",
		"/etc/passwd",
		"/home",
		"/tmp", // bare /tmp is no longer allowed; must scope to a root
		"/tmp/at-tasks/../",
		"/tmp/at-tasks/../etc",
		"/tmp/at-tasks-evil",
	} {
		rec := browseRequest(t, s, raw)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Errorf("path %q: want 403 or 404, got %d (%s)", raw, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// TestFileBrowseAPIServesAllowedRoot writes a file in a temp at-tasks
// subdirectory and confirms the browser returns it.
func TestFileBrowseAPIServesAllowedRoot(t *testing.T) {
	// Temporarily override allowed roots to the t.TempDir so we don't
	// pollute /tmp/at-tasks on the dev box. Restore via defer.
	tmp := t.TempDir()
	saveRoots := allowedFileBrowseRoots
	allowedFileBrowseRoots = []string{tmp}
	defer func() { allowedFileBrowseRoots = saveRoots }()

	if err := os.WriteFile(filepath.Join(tmp, "hello.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	s := &Server{}
	rec := browseRequest(t, s, tmp)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "hello.txt") {
		t.Errorf("response should list hello.txt; got: %s", body)
	}
	if !strings.Contains(body, "sub") {
		t.Errorf("response should list sub directory; got: %s", body)
	}
}

// TestFileServeAPIRejectsTraversal confirms the serve endpoint refuses
// to read files outside the allow-list. Even if the daemon UID can read
// the file, the response must be 403.
func TestFileServeAPIRejectsTraversal(t *testing.T) {
	s := &Server{}
	for _, raw := range []string{
		"/etc/passwd",
		"/etc/hostname",
		"/tmp/foo.txt",
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/files/serve?path="+raw, nil)
		rec := httptest.NewRecorder()
		s.FileServeAPI(rec, req)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Errorf("path %q: want 403 or 404, got %d", raw, rec.Code)
		}
	}
}

// TestFileServeAPIRangeSupport is the headline test for the video-scrubbing
// fix: a Range request must return 206 Partial Content with the requested
// bytes, and a no-Range request must return the full file with
// Accept-Ranges: bytes set.
func TestFileServeAPIRangeSupport(t *testing.T) {
	tmp := t.TempDir()
	saveRoots := allowedFileBrowseRoots
	allowedFileBrowseRoots = []string{tmp}
	defer func() { allowedFileBrowseRoots = saveRoots }()

	body := []byte("hello world, this is a test video stream payload")
	target := filepath.Join(tmp, "clip.mp4")
	if err := os.WriteFile(target, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := &Server{}

	// Full request — must get 200 with Accept-Ranges: bytes.
	t.Run("full request advertises Accept-Ranges", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/files/serve?path="+target, nil)
		rec := httptest.NewRecorder()
		s.FileServeAPI(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", rec.Code)
		}
		if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
			t.Errorf("want Accept-Ranges: bytes, got %q", got)
		}
		got, _ := io.ReadAll(rec.Body)
		if string(got) != string(body) {
			t.Errorf("body mismatch")
		}
	})

	// Range request — must return 206 with the requested slice.
	t.Run("range request returns 206 partial content", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/files/serve?path="+target, nil)
		req.Header.Set("Range", "bytes=6-10")
		rec := httptest.NewRecorder()
		s.FileServeAPI(rec, req)
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("want 206, got %d", rec.Code)
		}
		if cr := rec.Header().Get("Content-Range"); !strings.HasPrefix(cr, "bytes 6-10/") {
			t.Errorf("want Content-Range bytes 6-10/<size>, got %q", cr)
		}
		got, _ := io.ReadAll(rec.Body)
		want := body[6:11]
		if string(got) != string(want) {
			t.Errorf("range body = %q, want %q", got, want)
		}
	})

	// Open-ended Range — bytes=N-, common when seeking forward in <video>.
	t.Run("open-ended range", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/files/serve?path="+target, nil)
		req.Header.Set("Range", "bytes=20-")
		rec := httptest.NewRecorder()
		s.FileServeAPI(rec, req)
		if rec.Code != http.StatusPartialContent {
			t.Fatalf("want 206, got %d", rec.Code)
		}
		got, _ := io.ReadAll(rec.Body)
		want := body[20:]
		if string(got) != string(want) {
			t.Errorf("open-ended range body = %q, want %q", got, want)
		}
	})
}

// TestFileServeAPISetsContentType — extension-based MIME detection should
// still happen (so the browser knows it's video/mp4 and uses the native
// player rather than offering a download).
func TestFileServeAPISetsContentType(t *testing.T) {
	tmp := t.TempDir()
	saveRoots := allowedFileBrowseRoots
	allowedFileBrowseRoots = []string{tmp}
	defer func() { allowedFileBrowseRoots = saveRoots }()

	target := filepath.Join(tmp, "clip.mp4")
	if err := os.WriteFile(target, []byte("dummy"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := &Server{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/files/serve?path="+target, nil)
	rec := httptest.NewRecorder()
	s.FileServeAPI(rec, req)

	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "video/mp4") {
		t.Errorf("want Content-Type starting with video/mp4, got %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "inline") {
		t.Errorf("want Content-Disposition: inline; got %q", got)
	}
}

// TestFileDeleteAPIRejectsTraversalAndRoots — the delete endpoint is the
// most dangerous of the three. Confirm it refuses both out-of-allow-list
// paths and the allow-list roots themselves (so a misclick can't wipe
// /tmp/at-tasks for everyone).
func TestFileDeleteAPIRejectsTraversalAndRoots(t *testing.T) {
	s := &Server{}
	for _, raw := range []string{
		"/etc/passwd",
		"/tmp/foo",
		"/tmp/at-tasks",      // allow-list root
		"/tmp/at-sandbox",    // allow-list root
		"/tmp/at-org-abc123", // top-level dynamic root
	} {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/files?path="+raw, nil)
		rec := httptest.NewRecorder()
		s.FileDeleteAPI(rec, req)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Errorf("path %q: want 403 or 404, got %d (%s)", raw, rec.Code, strings.TrimSpace(rec.Body.String()))
		}
	}
}

// TestFileDeleteAPIDeletesAllowedFile — happy path, file inside an allowed
// root is deleted and a follow-up stat fails.
func TestFileDeleteAPIDeletesAllowedFile(t *testing.T) {
	tmp := t.TempDir()
	saveRoots := allowedFileBrowseRoots
	allowedFileBrowseRoots = []string{tmp}
	defer func() { allowedFileBrowseRoots = saveRoots }()

	target := filepath.Join(tmp, "scratch.png")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := &Server{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/files?path="+target, nil)
	rec := httptest.NewRecorder()
	s.FileDeleteAPI(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("file should be deleted, got err=%v", err)
	}
}
