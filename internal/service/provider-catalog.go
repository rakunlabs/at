package service

import "context"

type ProviderCatalogEntry struct {
	Key          string   `json:"key"`
	Type         string   `json:"type"`
	DefaultModel string   `json:"default_model"`
	Models       []string `json:"models"`
	Shared       bool     `json:"shared"`
}

type WorkspaceProviderCatalogStorer interface {
	ListWorkspaceProviderCatalog(context.Context) ([]ProviderCatalogEntry, error)
}
