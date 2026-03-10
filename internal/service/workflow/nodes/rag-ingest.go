package nodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/at/internal/service/workflow"
	"github.com/rakunlabs/logi"
)

// ragIngestNode consumes the output of a git_fetch node (or similar file source)
// and ingests the files into a RAG collection. It handles deleting stale chunks
// for modified/deleted files and updating the sync state variable.
//
// Config (node.Data):
//
//	collection_id string — target RAG collection ID (required)
//
// Inputs:
//
//	files         []map[string]any — changed/added files [{path, content, status}]
//	deleted_files []string         — files removed since last sync
//	commit_sha    string           — HEAD commit SHA (optional, for tracking)
//	repo_url      string           — repository URL (optional, for metadata)
//	branch        string           — branch name (optional, for metadata)
//	variable_key  string           — variable key to update with commit_sha (optional)
//
// Outputs:
//
//	chunks_added    int — number of new chunks ingested
//	files_processed int — number of files processed
//	deleted_count   int — number of files whose chunks were deleted
type ragIngestNode struct {
	collectionID string
}

func init() {
	workflow.RegisterNodeType("rag_ingest", newRagIngestNode)
}

func newRagIngestNode(node service.WorkflowNode) (workflow.Noder, error) {
	n := &ragIngestNode{}
	if v, ok := node.Data["collection_id"].(string); ok {
		n.collectionID = v
	}
	return n, nil
}

func (n *ragIngestNode) Type() string { return "rag_ingest" }

func (n *ragIngestNode) Validate(_ context.Context, reg *workflow.Registry) error {
	if n.collectionID == "" {
		return fmt.Errorf("rag_ingest: 'collection_id' is required")
	}
	if reg.RAGIngestFile == nil {
		return fmt.Errorf("rag_ingest: RAG service not available")
	}
	return nil
}

func (n *ragIngestNode) Run(ctx context.Context, reg *workflow.Registry, inputs map[string]any) (workflow.NodeResult, error) {
	// Parse inputs.
	var files []map[string]any
	if v, ok := inputs["files"].([]any); ok {
		// Convert []any to []map[string]any (common when coming from JSON unmarshal or loose typing)
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				files = append(files, m)
			}
		}
	} else if v, ok := inputs["files"].([]map[string]any); ok {
		files = v
	}

	var deletedFiles []string
	if v, ok := inputs["deleted_files"].([]any); ok {
		for _, item := range v {
			if s, ok := item.(string); ok {
				deletedFiles = append(deletedFiles, s)
			}
		}
	} else if v, ok := inputs["deleted_files"].([]string); ok {
		deletedFiles = v
	}

	commitSHA, _ := inputs["commit_sha"].(string)
	repoURL, _ := inputs["repo_url"].(string)
	branch, _ := inputs["branch"].(string)
	variableKey, _ := inputs["variable_key"].(string)

	chunksAdded := 0
	filesProcessed := 0
	deletedCount := 0

	// 1. Handle deletions (deleted files + modified files which need re-ingestion).
	// For modified files, we delete old chunks first.
	filesToDelete := make(map[string]bool)
	for _, f := range deletedFiles {
		filesToDelete[f] = true
	}
	for _, f := range files {
		path, _ := f["path"].(string)
		status, _ := f["status"].(string)
		if path != "" && (status == "modified" || status == "changed") {
			filesToDelete[path] = true
		}
	}

	if len(filesToDelete) > 0 {
		if reg.RAGDeleteBySource != nil {
			for path := range filesToDelete {
				// Construct the source identifier used during ingestion.
				// If repo_url is present, we prefix it to make the source globally unique.
				source := path
				if repoURL != "" {
					if strings.HasSuffix(repoURL, "/") {
						source = repoURL + path
					} else {
						source = repoURL + "/" + path
					}
				}

				if err := reg.RAGDeleteBySource(ctx, n.collectionID, source); err != nil {
					// Log error but continue. Some vector stores don't support delete-by-metadata.
					logi.Ctx(ctx).Warn("rag_ingest: failed to delete stale chunks",
						"source", source,
						"error", err,
					)
				} else {
					deletedCount++
				}
			}
		} else {
			logi.Ctx(ctx).Warn("rag_ingest: skipping deletions (RAGDeleteBySource not available)")
		}
	}

	// 2. Ingest new/modified files.
	for _, f := range files {
		path, _ := f["path"].(string)
		contentStr, _ := f["content"].(string)

		if path == "" || contentStr == "" {
			continue
		}

		source := path
		if repoURL != "" {
			if strings.HasSuffix(repoURL, "/") {
				source = repoURL + path
			} else {
				source = repoURL + "/" + path
			}
		}

		extraMetadata := make(map[string]any)
		if commitSHA != "" {
			extraMetadata["commit_sha"] = commitSHA
		}
		if repoURL != "" {
			extraMetadata["repo_url"] = repoURL
		}
		if branch != "" {
			extraMetadata["branch"] = branch
		}
		extraMetadata["path"] = path

		count, err := reg.RAGIngestFile(ctx, n.collectionID, []byte(contentStr), source, extraMetadata)
		if err != nil {
			return nil, fmt.Errorf("rag_ingest: ingest %s: %w", path, err)
		}
		chunksAdded += count
		filesProcessed++

		// Store original content in rag_pages.
		if reg.RAGPageUpsert != nil {
			if pageErr := reg.RAGPageUpsert(ctx, n.collectionID, source, path, contentStr, "", extraMetadata); pageErr != nil {
				logi.Ctx(ctx).Warn("rag_ingest: failed to store page", "path", path, "error", pageErr)
			}
		}

		// Log progress for large batches?
		if filesProcessed%10 == 0 {
			logi.Ctx(ctx).Info("rag_ingest: progress", "processed", filesProcessed, "total", len(files))
		}
	}

	// 3. Update sync state variable.
	if variableKey != "" && commitSHA != "" && reg.RAGStateSave != nil {
		if err := reg.RAGStateSave(ctx, variableKey, commitSHA); err != nil {
			// Don't fail the whole workflow if variable save fails, but log it.
			// Actually, if we don't save, the next run will re-ingest everything.
			// It's safer to return error here to ensure data consistency?
			// Or maybe just warn.
			logi.Ctx(ctx).Error("rag_ingest: failed to save sync state variable",
				"key", variableKey,
				"value", commitSHA,
				"error", err,
			)
			// Return error to alert the user/system.
			return nil, fmt.Errorf("rag_ingest: save rag state %q: %w", variableKey, err)
		}
	}

	return workflow.NewResult(map[string]any{
		"chunks_added":    chunksAdded,
		"files_processed": filesProcessed,
		"deleted_count":   deletedCount,
	}), nil
}
