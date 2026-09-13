package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
)

type botVideoTemplateSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListBotVideoTemplatesAPI is an installation-admin catalog. It returns only
// the names/IDs needed by the picker, from the
// same fixed asset library used by createTelegramVideoTask. No client-supplied
// paths, arbitrary file browsing or host execution permission are needed.
func (s *Server) ListBotVideoTemplatesAPI(w http.ResponseWriter, r *http.Request) {
	actor, admitted := service.AccessPrincipalFromContext(r.Context())
	if !service.LegacyWorkspaceAccessFromContext(r.Context()) && (!admitted || !actor.PlatformAdmin) {
		httpResponse(w, "administrator authentication required", http.StatusForbidden)
		return
	}
	items, err := listBotVideoTemplates(workflow.AssetsDir())
	if err != nil {
		slog.Error("failed to load bot video template catalog", "error", err)
		httpResponse(w, "Long Video templates could not be loaded. Check that the Studio asset library is accessible.", http.StatusServiceUnavailable)
		return
	}
	httpResponseJSON(w, map[string]any{"items": items}, http.StatusOK)
}

func listBotVideoTemplates(assets string) ([]botVideoTemplateSummary, error) {
	items := []botVideoTemplateSummary{}
	root, err := os.OpenRoot(assets)
	if errors.Is(err, os.ErrNotExist) {
		return items, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open assets: %w", err)
	}
	defer root.Close()
	dir, err := openStudioVideoRoot(root, "video-templates")
	if errors.Is(err, os.ErrNotExist) {
		return items, nil
	}
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	f, err := dir.Open(".")
	if err != nil {
		return nil, fmt.Errorf("open template directory: %w", err)
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("list templates: %w", err)
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !studioVideoID.MatchString(id) {
			continue
		}
		data, err := readStudioVideoJSON(dir, entry.Name())
		if err != nil {
			slog.Warn("skip unreadable video template", "id", id, "error", err)
			continue
		}
		template, err := decodeVideoTemplate(data, id)
		if err != nil {
			slog.Warn("skip invalid video template", "id", id, "error", err)
			continue
		}
		items = append(items, botVideoTemplateSummary{ID: id, Name: template.Name})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Name == items[j].Name {
			return items[i].ID < items[j].ID
		}
		return items[i].Name < items[j].Name
	})
	return items, nil
}
