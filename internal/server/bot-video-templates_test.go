package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBotVideoTemplateCatalog(t *testing.T) {
	_, assets, template, _ := studioVideoFixture(t)
	studioVideoWrite(t, filepath.Join(assets, "video-templates", "invalid.json"), map[string]string{"name": "invalid"})
	if err := os.Symlink(filepath.Join(assets, "video-templates", template.ID+".json"), filepath.Join(assets, "video-templates", "linked.json")); err != nil {
		t.Fatal(err)
	}
	items, err := listBotVideoTemplates(assets)
	if err != nil || len(items) != 1 || items[0].ID != template.ID || items[0].Name != template.Name {
		t.Fatalf("catalog: %+v %v", items, err)
	}
	data, err := json.Marshal(items)
	if err != nil || strings.Contains(string(data), "brief") || strings.Contains(string(data), assets) {
		t.Fatalf("catalog exposed template content/path: %s %v", data, err)
	}
	if empty, err := listBotVideoTemplates(t.TempDir()); err != nil || len(empty) != 0 {
		t.Fatalf("missing template folder: %+v %v", empty, err)
	}
	s := &Server{}
	w := httptest.NewRecorder()
	s.ListBotVideoTemplatesAPI(w, httptest.NewRequest(http.MethodGet, "/api/v1/bots/video-templates", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("anonymous catalog: %d", w.Code)
	}
}
