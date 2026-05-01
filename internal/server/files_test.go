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

// browseRequest is a tiny helper for hitting FileBrowseAPI in tests.
func browseRequest(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/api/v1/files/browse?path=" + path
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	s.FileBrowseAPI(rec, req)
	return rec
}

// TestFileBrowseAPIRejectsMissing — paths that don't exist must return 404,
// regardless of the previous allow-list. We no longer block reads outside
// /tmp/at-*; the only failure mode is "doesn't exist" or "not a directory".
func TestFileBrowseAPIRejectsMissing(t *testing.T) {
	s := &Server{}
	rec := browseRequest(t, s, "/this/does/not/exist/hopefully")
	if rec.Code != http.StatusNotFound {
		t.Errorf("want 404 for missing path, got %d (%s)", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// TestFileBrowseAPIServesArbitraryDir confirms the browser will list any
// directory the daemon UID can read — no allow-list anymore.
func TestFileBrowseAPIServesArbitraryDir(t *testing.T) {
	tmp := t.TempDir()

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

// TestFileServeAPIRangeSupport is the headline test for the video-scrubbing
// fix: a Range request must return 206 Partial Content with the requested
// bytes, and a no-Range request must return the full file with
// Accept-Ranges: bytes set.
func TestFileServeAPIRangeSupport(t *testing.T) {
	tmp := t.TempDir()

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

// TestFileDeleteAPIRejectsRoot — the only thing the delete endpoint still
// refuses is removing the filesystem root.
func TestFileDeleteAPIRejectsRoot(t *testing.T) {
	s := &Server{}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/files?path=/", nil)
	rec := httptest.NewRecorder()
	s.FileDeleteAPI(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("want 403 for delete /, got %d (%s)", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

// TestFileDeleteAPIDeletesArbitraryFile — happy path, any file the daemon
// can write to is now deletable.
func TestFileDeleteAPIDeletesArbitraryFile(t *testing.T) {
	tmp := t.TempDir()

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
