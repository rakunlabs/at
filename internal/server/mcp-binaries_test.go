package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rakunlabs/at/internal/service/workflow"
)

func configureTestMCPDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := workflow.ConfigureMCPDir(dir); err != nil {
		t.Fatalf("ConfigureMCPDir: %v", err)
	}
	t.Cleanup(func() { _ = workflow.ConfigureMCPDir("") })
	return filepath.Join(dir, "mcps")
}

func uploadMCPBinary(t *testing.T, s *Server, name, content string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/binaries", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	s.UploadMCPBinaryAPI(rr, req)
	return rr
}

func TestMCPBinariesUploadListDelete(t *testing.T) {
	dir := configureTestMCPDir(t)
	s := &Server{}

	// Upload: defaults to executable.
	rr := uploadMCPBinary(t, s, "my-mcp", "#!/bin/sh\necho hi\n", nil)
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload = %d: %s", rr.Code, rr.Body.String())
	}
	info, err := os.Stat(filepath.Join(dir, "my-mcp"))
	if err != nil {
		t.Fatalf("stat uploaded binary: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("uploaded binary mode = %v, want executable", info.Mode())
	}

	// Upload a config file: executable=false → 0644.
	rr = uploadMCPBinary(t, s, "tool-config.json", `{"a":1}`, map[string]string{"executable": "false"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("config upload = %d: %s", rr.Code, rr.Body.String())
	}
	info, err = os.Stat(filepath.Join(dir, "tool-config.json"))
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Fatalf("config mode = %v, want non-executable", info.Mode())
	}

	// List reports both, with the library dir for command references.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mcp/binaries", nil)
	rr = httptest.NewRecorder()
	s.ListMCPBinariesAPI(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list = %d: %s", rr.Code, rr.Body.String())
	}
	var list struct {
		Dir   string          `json:"dir"`
		Files []mcpBinaryInfo `json:"files"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("parse list: %v", err)
	}
	if list.Dir != dir {
		t.Fatalf("list dir = %q, want %q", list.Dir, dir)
	}
	if len(list.Files) != 2 {
		t.Fatalf("list files = %d, want 2 (%+v)", len(list.Files), list.Files)
	}
	byName := map[string]mcpBinaryInfo{}
	for _, f := range list.Files {
		byName[f.Name] = f
	}
	if !byName["my-mcp"].Executable || byName["tool-config.json"].Executable {
		t.Fatalf("executable flags wrong: %+v", byName)
	}

	// Delete.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/mcp/binaries/my-mcp", nil)
	req.SetPathValue("name", "my-mcp")
	rr = httptest.NewRecorder()
	s.DeleteMCPBinaryAPI(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete = %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "my-mcp")); !os.IsNotExist(err) {
		t.Fatalf("binary should be gone, stat err = %v", err)
	}

	// Delete of a missing file is 404.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/mcp/binaries/gone", nil)
	req.SetPathValue("name", "gone")
	rr = httptest.NewRecorder()
	s.DeleteMCPBinaryAPI(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("delete missing = %d, want 404", rr.Code)
	}
}

type tarEntry struct {
	name     string
	body     string
	mode     int64
	typeFlag byte
	link     string
}

func buildTarGz(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typeFlag
		if typ == 0 {
			typ = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: typ, Linkname: e.link}
		if typ == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("tar body: %v", err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func TestMCPBinariesArchiveExtraction(t *testing.T) {
	dir := configureTestMCPDir(t)
	s := &Server{}

	archive := buildTarGz(t, []tarEntry{
		{name: "foo-mcp/", typeFlag: tar.TypeDir, mode: 0o755},
		{name: "foo-mcp/bin/foo", body: "#!/bin/sh\necho foo\n", mode: 0o755},
		{name: "foo-mcp/config.json", body: `{"a":1}`, mode: 0o644},
		{name: "foo-mcp/link", typeFlag: tar.TypeSymlink, link: "/etc/passwd"}, // skipped
	})

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "foo-mcp-v1.2.tar.gz")
	fw.Write(archive)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/binaries", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	s.UploadMCPBinaryAPI(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("archive upload = %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Name      string `json:"name"`
		Path      string `json:"path"`
		Files     int    `json:"files"`
		Extracted bool   `json:"extracted"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if resp.Name != "foo-mcp-v1.2" || !resp.Extracted || resp.Files != 2 {
		t.Fatalf("archive response = %+v", resp)
	}

	// Exec bit preserved on bin/foo, not on config.json; symlink skipped.
	bin := filepath.Join(dir, "foo-mcp-v1.2", "foo-mcp", "bin", "foo")
	info, err := os.Stat(bin)
	if err != nil {
		t.Fatalf("stat extracted binary: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("extracted binary mode = %v, want executable", info.Mode())
	}
	cfg, err := os.Stat(filepath.Join(dir, "foo-mcp-v1.2", "foo-mcp", "config.json"))
	if err != nil {
		t.Fatalf("stat extracted config: %v", err)
	}
	if cfg.Mode().Perm()&0o111 != 0 {
		t.Fatalf("config mode = %v, want non-executable", cfg.Mode())
	}
	if _, err := os.Lstat(filepath.Join(dir, "foo-mcp-v1.2", "foo-mcp", "link")); !os.IsNotExist(err) {
		t.Fatalf("symlink should be skipped, lstat err = %v", err)
	}

	// The list reports the extracted root as a directory entry.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/mcp/binaries", nil)
	rr = httptest.NewRecorder()
	s.ListMCPBinariesAPI(rr, req)
	var list struct {
		Files []mcpBinaryInfo `json:"files"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &list); err != nil {
		t.Fatalf("parse list: %v", err)
	}
	if len(list.Files) != 1 || !list.Files[0].Dir || list.Files[0].Name != "foo-mcp-v1.2" {
		t.Fatalf("list after extraction = %+v", list.Files)
	}

	// Delete removes the whole directory.
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/mcp/binaries/foo-mcp-v1.2", nil)
	req.SetPathValue("name", "foo-mcp-v1.2")
	rr = httptest.NewRecorder()
	s.DeleteMCPBinaryAPI(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete extracted dir = %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "foo-mcp-v1.2")); !os.IsNotExist(err) {
		t.Fatalf("extracted dir should be gone, err = %v", err)
	}
}

func TestMCPBinariesArchiveTraversalRejected(t *testing.T) {
	dir := configureTestMCPDir(t)
	s := &Server{}

	archive := buildTarGz(t, []tarEntry{
		{name: "ok.txt", body: "fine", mode: 0o644},
		{name: "../evil.txt", body: "nope", mode: 0o644},
	})
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "evil.tgz")
	fw.Write(archive)
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/binaries", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	s.UploadMCPBinaryAPI(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("traversal archive = %d, want 400: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.txt")); !os.IsNotExist(err) {
		t.Fatalf("traversal file must not exist outside the library, err = %v", err)
	}
	// Failed extraction leaves no partial target and no temp litter.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read library: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("library should be empty after failed extraction, got %v", entries)
	}
}

func TestMCPBinariesArchiveStoredRawWhenExtractFalse(t *testing.T) {
	dir := configureTestMCPDir(t)
	s := &Server{}

	archive := buildTarGz(t, []tarEntry{{name: "a.txt", body: "x", mode: 0o644}})
	rr := uploadMCPBinaryRaw(t, s, "keep.tar.gz", archive, map[string]string{"extract": "false", "executable": "false"})
	if rr.Code != http.StatusCreated {
		t.Fatalf("raw archive upload = %d: %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "keep.tar.gz")); err != nil {
		t.Fatalf("raw archive should be stored as a file: %v", err)
	}
}

func uploadMCPBinaryRaw(t *testing.T, s *Server, name string, content []byte, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mcp/binaries", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rr := httptest.NewRecorder()
	s.UploadMCPBinaryAPI(rr, req)
	return rr
}

func TestMCPBinariesNameValidation(t *testing.T) {
	configureTestMCPDir(t)
	s := &Server{}

	for _, name := range []string{"../evil", "a/b", `a\b`, ".", "..", ".hidden"} {
		rr := uploadMCPBinary(t, s, "x", "data", map[string]string{"name": name})
		if rr.Code != http.StatusBadRequest {
			t.Errorf("upload name %q = %d, want 400", name, rr.Code)
		}

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/mcp/binaries/x", nil)
		req.SetPathValue("name", name)
		del := httptest.NewRecorder()
		s.DeleteMCPBinaryAPI(del, req)
		if del.Code != http.StatusBadRequest {
			t.Errorf("delete name %q = %d, want 400", name, del.Code)
		}
	}
}
