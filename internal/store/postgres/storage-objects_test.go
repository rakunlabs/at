package postgres

import (
	"errors"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestStorageObjectsWorkspaceNamespaceAndReplacement(t *testing.T) {
	p, ctx, workspace, admin := workspaceFixture(t)
	other, err := p.CreateWorkspace(ctx, "Storage sibling", admin.ID)
	if err != nil {
		t.Fatal(err)
	}

	first, err := p.PutStorageObject(ctx, service.StoredObject{
		WorkspaceID: workspace.ID,
		OwnerUserID: admin.ID,
		Namespace:   service.StorageNamespaceFiles,
		Path:        "docs/readme.md",
		Backend:     service.StorageBackendFilesystem,
		StorageKey:  "workspaces/one/files/docs/readme.md",
		ContentType: "text/markdown",
		SizeBytes:   5,
		Checksum:    "first",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == "" || first.CreatedAt == "" || first.UpdatedAt == "" {
		t.Fatalf("incomplete object: %+v", first)
	}

	replaced, err := p.PutStorageObject(ctx, service.StoredObject{
		WorkspaceID: workspace.ID,
		OwnerUserID: admin.ID,
		Namespace:   service.StorageNamespaceFiles,
		Path:        "docs/readme.md",
		Backend:     service.StorageBackendS3,
		StorageKey:  "new-key",
		ContentType: "text/plain",
		SizeBytes:   9,
		Checksum:    "second",
	})
	if err != nil {
		t.Fatal(err)
	}
	if replaced.ID != first.ID || replaced.CreatedAt != first.CreatedAt || replaced.StorageKey != "new-key" || replaced.Checksum != "second" {
		t.Fatalf("replacement: first=%+v replaced=%+v", first, replaced)
	}

	for _, object := range []service.StoredObject{
		{WorkspaceID: workspace.ID, Namespace: service.StorageNamespaceAssets, Path: "docs/readme.md", Backend: service.StorageBackendFilesystem, StorageKey: "asset-key", ContentType: "text/plain"},
		{WorkspaceID: other.ID, Namespace: service.StorageNamespaceFiles, Path: "docs/readme.md", Backend: service.StorageBackendFilesystem, StorageKey: "other-key", ContentType: "text/plain"},
		{WorkspaceID: workspace.ID, Namespace: service.StorageNamespaceFiles, Path: "notes/todo.txt", Backend: service.StorageBackendFilesystem, StorageKey: "todo-key", ContentType: "text/plain"},
	} {
		if _, err := p.PutStorageObject(ctx, object); err != nil {
			t.Fatal(err)
		}
	}

	listed, err := p.ListStorageObjects(ctx, workspace.ID, service.StorageNamespaceFiles, "docs/")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("scoped list: %+v", listed)
	}
	got, err := p.GetStorageObject(ctx, other.ID, service.StorageNamespaceFiles, "docs/readme.md")
	if err != nil || got.StorageKey != "other-key" {
		t.Fatalf("sibling object: %+v %v", got, err)
	}

	deleted, err := p.DeleteStorageObject(ctx, workspace.ID, service.StorageNamespaceFiles, "docs/readme.md")
	if err != nil || deleted.ID != first.ID {
		t.Fatalf("delete: %+v %v", deleted, err)
	}
	if _, err := p.GetStorageObject(ctx, workspace.ID, service.StorageNamespaceFiles, "docs/readme.md"); !errors.Is(err, service.ErrStorageObjectNotFound) {
		t.Fatalf("deleted object read: %v", err)
	}
	if _, err := p.DeleteStorageObject(ctx, workspace.ID, service.StorageNamespaceFiles, "docs/readme.md"); !errors.Is(err, service.ErrStorageObjectNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestStorageObjectRequiresAddress(t *testing.T) {
	p := newTestStore(t, nil)
	for name, object := range map[string]service.StoredObject{
		"workspace": {Namespace: service.StorageNamespaceFiles, Path: "a", StorageKey: "k"},
		"namespace": {WorkspaceID: service.DefaultWorkspaceID, Path: "a", StorageKey: "k"},
		"path":      {WorkspaceID: service.DefaultWorkspaceID, Namespace: service.StorageNamespaceFiles, StorageKey: "k"},
		"key":       {WorkspaceID: service.DefaultWorkspaceID, Namespace: service.StorageNamespaceFiles, Path: "a"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := p.PutStorageObject(t.Context(), object); !errors.Is(err, service.ErrStorageObjectNotFound) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}
