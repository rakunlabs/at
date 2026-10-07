package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/executiontest"
	"github.com/rakunlabs/at/internal/store/postgres/postgrestest"
)

func storageFileUploadRequest(t *testing.T, filePath, content string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", filePath[strings.LastIndex(filePath, "/")+1:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(content)); err != nil {
		t.Fatal(err)
	}
	if dir, _, ok := strings.Cut(filePath, "/"); ok {
		if err := form.WriteField("path", dir); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/storage/files/upload", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	return r.WithContext(executiontest.Context(t))
}

func TestStorageFilesDurableRoundTrip(t *testing.T) {
	store := postgrestest.New(t, nil)
	root := t.TempDir()
	if _, err := store.SaveStorageSettings(t.Context(), service.StorageSettings{
		Version: 1, Backend: service.StorageBackendFilesystem,
		Filesystem: service.StorageFilesystemSettings{Root: root},
	}); err != nil {
		t.Fatal(err)
	}
	s := &Server{store: store}

	w := httptest.NewRecorder()
	s.StorageFileUploadAPI(w, storageFileUploadRequest(t, "docs/readme.txt", "durable file"))
	if w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/storage/files/browse?path=.", nil).WithContext(executiontest.Context(t))
	w = httptest.NewRecorder()
	s.StorageFileBrowseAPI(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("browse root: %d %s", w.Code, w.Body.String())
	}
	var rootList struct {
		Entries []fileEntry `json:"entries"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rootList); err != nil {
		t.Fatal(err)
	}
	if len(rootList.Entries) != 1 || rootList.Entries[0].Name != "docs" || !rootList.Entries[0].IsDir {
		t.Fatalf("root entries: %+v", rootList.Entries)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/storage/files/browse?path=docs", nil).WithContext(executiontest.Context(t))
	w = httptest.NewRecorder()
	s.StorageFileBrowseAPI(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"readme.txt"`) {
		t.Fatalf("browse docs: %d %s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/storage/files/serve?path=docs/readme.txt", nil).WithContext(executiontest.Context(t))
	r.Header.Set("Range", "bytes=0-6")
	w = httptest.NewRecorder()
	s.StorageFileServeAPI(w, r)
	if w.Code != http.StatusPartialContent || w.Body.String() != "durable" {
		t.Fatalf("serve range: %d %q", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodDelete, "/api/v1/storage/files?path=docs/readme.txt", nil).WithContext(executiontest.Context(t))
	w = httptest.NewRecorder()
	s.StorageFileDeleteAPI(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/api/v1/storage/files/serve?path=docs/readme.txt", nil).WithContext(executiontest.Context(t))
	w = httptest.NewRecorder()
	s.StorageFileServeAPI(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("serve deleted: %d %s", w.Code, w.Body.String())
	}
	// Files added outside AT are visible without inserting a catalog row.
	principal, _, _ := service.ExecutionFromContext(executiontest.Context(t))
	dir := filepath.Join(root, "workspaces", principal.WorkspaceID, "files", "docs")
	if err := os.WriteFile(filepath.Join(dir, "external.txt"), []byte("outside AT"), 0600); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/storage/files/browse?path=docs", nil).WithContext(executiontest.Context(t))
	w = httptest.NewRecorder()
	s.StorageFileBrowseAPI(w, r)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"external.txt"`) {
		t.Fatalf("external browse: %d %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest(http.MethodGet, "/api/v1/storage/files/serve?path=docs/external.txt", nil).WithContext(executiontest.Context(t))
	w = httptest.NewRecorder()
	s.StorageFileServeAPI(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "outside AT" {
		t.Fatalf("external serve: %d %s", w.Code, w.Body.String())
	}
	r = httptest.NewRequest(http.MethodDelete, "/api/v1/storage/files?path=docs/external.txt", nil).WithContext(executiontest.Context(t))
	w = httptest.NewRecorder()
	s.StorageFileDeleteAPI(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("external delete: %d %s", w.Code, w.Body.String())
	}
}

func TestNormalizeStorageFilePath(t *testing.T) {
	for _, bad := range []string{"/etc/passwd", "../secret", "a/../secret", "a//b", `a\b`} {
		if _, err := normalizeStorageFilePath(bad, false); err == nil {
			t.Errorf("accepted unsafe path %q", bad)
		}
	}
	if got, err := normalizeStorageFilePath("docs/readme.md", false); err != nil || got != "docs/readme.md" {
		t.Fatalf("normal path = %q, %v", got, err)
	}
}
