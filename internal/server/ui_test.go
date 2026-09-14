package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	mfolder "github.com/rakunlabs/ada/handler/folder"
)

func TestUIUpdateCachePolicy(t *testing.T) {
	for _, base := range []string{"", "/at"} {
		t.Run("base="+base, func(t *testing.T) {
			files := fstest.MapFS{}
			for _, path := range []string{"index.html", "manifest.webmanifest", "workspace-media.js", "offline.html", "favicon.ico", "brand/favicon.svg", "brand/favicon-192x192.png", "assets/app-hashed.js"} {
				files[path] = &fstest.MapFile{Data: []byte("fixture")}
			}
			h, err := mfolder.New(uiFolderConfig(base))
			if err != nil {
				t.Fatal(err)
			}
			h.SetFs(http.FS(files))
			for path := range files {
				requestPath := path
				if path == "index.html" {
					requestPath = ""
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"/"+requestPath, nil))
				if w.Code != http.StatusOK {
					t.Fatalf("%s: status %d", path, w.Code)
				}
				if strings.HasPrefix(path, "assets/") {
					continue
				}
				want := "no-cache"
				if path == "index.html" {
					want = "no-store"
				}
				if got := w.Header().Get("Cache-Control"); got != want {
					t.Errorf("%s: cache %q, want %q", path, got, want)
				}
			}
		})
	}
}

func TestUIEmbeddedPWAAssets(t *testing.T) {
	files, err := fs.Sub(uiFS, "dist")
	if err != nil {
		t.Fatal(err)
	}
	body, err := fs.ReadFile(files, "manifest.webmanifest")
	if err != nil {
		t.Skip("UI not built; run make build-ui to verify embedded PWA assets")
	}
	var manifest struct {
		Icons []struct {
			Src string `json:"src"`
		} `json:"icons"`
	}
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Icons) == 0 {
		t.Fatal("embedded manifest has no icons")
	}
	for _, icon := range manifest.Icons {
		data, err := fs.ReadFile(files, icon.Src)
		if err != nil || len(data) == 0 {
			t.Fatalf("embedded icon %s unavailable: %v", icon.Src, err)
		}
	}
	for _, path := range []string{"index.html", "workspace-media.js", "offline.html", "brand/favicon.svg", "brand/favicon.ico"} {
		data, err := fs.ReadFile(files, path)
		if err != nil || len(data) == 0 {
			t.Fatalf("embedded file %s unavailable: %v", path, err)
		}
		if strings.Contains(string(data), "__AT_BRAND_LOGO__") {
			t.Fatalf("%s contains unresolved branding", path)
		}
	}
}
