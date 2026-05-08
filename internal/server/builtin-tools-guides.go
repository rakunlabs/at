package server

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rakunlabs/at/internal/service"
)

// ─── Guide Tool Executors (Phase 2) ───
//
// Guides are user-authored markdown documents shown in the AT UI's
// docs section. No sensitive fields, no redaction, no side effects.
// Pure CRUD over GuideStorer.

func (s *Server) execGuideList(ctx context.Context, _ map[string]any) (string, error) {
	if s.guideStore == nil {
		return "", fmt.Errorf("guide store not configured")
	}
	records, err := s.guideStore.ListGuides(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("list guides: %w", err)
	}
	if records == nil {
		records = &service.ListResult[service.Guide]{Data: []service.Guide{}}
	}
	out, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal guides: %w", err)
	}
	return string(out), nil
}

func (s *Server) execGuideGet(ctx context.Context, args map[string]any) (string, error) {
	if s.guideStore == nil {
		return "", fmt.Errorf("guide store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	rec, err := s.guideStore.GetGuide(ctx, id)
	if err != nil {
		return "", fmt.Errorf("get guide %q: %w", id, err)
	}
	if rec == nil {
		return "", fmt.Errorf("guide %q not found", id)
	}
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal guide: %w", err)
	}
	return string(out), nil
}

func (s *Server) execGuideCreate(ctx context.Context, args map[string]any) (string, error) {
	if s.guideStore == nil {
		return "", fmt.Errorf("guide store not configured")
	}
	title := stringArg(args, "title")
	if title == "" {
		return "", fmt.Errorf("title is required")
	}
	rec, err := s.guideStore.CreateGuide(ctx, service.Guide{
		Title:       title,
		Description: stringArg(args, "description"),
		Icon:        stringArg(args, "icon"),
		Content:     stringArg(args, "content"),
		CreatedBy:   "mcp",
		UpdatedBy:   "mcp",
	})
	if err != nil {
		return "", fmt.Errorf("create guide: %w", err)
	}
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal guide: %w", err)
	}
	return string(out), nil
}

func (s *Server) execGuideUpdate(ctx context.Context, args map[string]any) (string, error) {
	if s.guideStore == nil {
		return "", fmt.Errorf("guide store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	title := stringArg(args, "title")
	if title == "" {
		return "", fmt.Errorf("title is required")
	}
	rec, err := s.guideStore.UpdateGuide(ctx, id, service.Guide{
		Title:       title,
		Description: stringArg(args, "description"),
		Icon:        stringArg(args, "icon"),
		Content:     stringArg(args, "content"),
		UpdatedBy:   "mcp",
	})
	if err != nil {
		return "", fmt.Errorf("update guide %q: %w", id, err)
	}
	if rec == nil {
		return "", fmt.Errorf("guide %q not found", id)
	}
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal guide: %w", err)
	}
	return string(out), nil
}

func (s *Server) execGuideDelete(ctx context.Context, args map[string]any) (string, error) {
	if s.guideStore == nil {
		return "", fmt.Errorf("guide store not configured")
	}
	id, _ := args["id"].(string)
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	if err := s.guideStore.DeleteGuide(ctx, id); err != nil {
		return "", fmt.Errorf("delete guide %q: %w", id, err)
	}
	return fmt.Sprintf(`{"status":"deleted","id":%q}`, id), nil
}
