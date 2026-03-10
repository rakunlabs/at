package sqlite3

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

// ─── RAG Page CRUD ───

type ragPageRow struct {
	ID           string `db:"id"`
	CollectionID string `db:"collection_id"`
	Source       string `db:"source"`
	Path         string `db:"path"`
	Content      string `db:"content"`
	ContentType  string `db:"content_type"`
	Metadata     string `db:"metadata"`
	ContentHash  string `db:"content_hash"`
	CreatedAt    string `db:"created_at"`
	UpdatedAt    string `db:"updated_at"`
}

func ragPageRowToRecord(row ragPageRow) (*service.RAGPage, error) {
	var metadata map[string]any
	if row.Metadata != "" && row.Metadata != "{}" {
		if err := json.Unmarshal([]byte(row.Metadata), &metadata); err != nil {
			return nil, fmt.Errorf("unmarshal rag page metadata for %q: %w", row.ID, err)
		}
	}

	return &service.RAGPage{
		ID:           row.ID,
		CollectionID: row.CollectionID,
		Source:       row.Source,
		Path:         row.Path,
		Content:      row.Content,
		ContentType:  row.ContentType,
		Metadata:     metadata,
		ContentHash:  row.ContentHash,
		CreatedAt:    row.CreatedAt,
		UpdatedAt:    row.UpdatedAt,
	}, nil
}

var ragPageSelectColumns = []any{"id", "collection_id", "source", "path", "content", "content_type", "metadata", "content_hash", "created_at", "updated_at"}

func scanRAGPageRow(scanner interface{ Scan(...any) error }) (*ragPageRow, error) {
	var row ragPageRow
	if err := scanner.Scan(&row.ID, &row.CollectionID, &row.Source, &row.Path, &row.Content, &row.ContentType, &row.Metadata, &row.ContentHash, &row.CreatedAt, &row.UpdatedAt); err != nil {
		return nil, err
	}
	return &row, nil
}

func (s *SQLite) ListRAGPages(ctx context.Context, collectionID string, q *query.Query) (*service.ListResult[service.RAGPage], error) {
	countSQL, _, err := s.goqu.From(s.tableRAGPages).
		Select(goqu.COUNT("*")).
		Where(goqu.I("collection_id").Eq(collectionID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build count rag pages query: %w", err)
	}

	var total uint64
	if err := s.db.QueryRowContext(ctx, countSQL).Scan(&total); err != nil {
		return nil, fmt.Errorf("count rag pages: %w", err)
	}

	ds := s.goqu.From(s.tableRAGPages).
		Select(ragPageSelectColumns...).
		Where(goqu.I("collection_id").Eq(collectionID)).
		Order(goqu.I("path").Asc())

	offset, limit := getPagination(q)
	if limit > 0 {
		ds = ds.Limit(uint(limit))
	}
	if offset > 0 {
		ds = ds.Offset(uint(offset))
	}

	sqlStr, _, err := ds.ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build list rag pages query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sqlStr)
	if err != nil {
		return nil, fmt.Errorf("list rag pages: %w", err)
	}
	defer rows.Close()

	var result []service.RAGPage
	for rows.Next() {
		row, err := scanRAGPageRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan rag page row: %w", err)
		}

		rec, err := ragPageRowToRecord(*row)
		if err != nil {
			return nil, err
		}
		result = append(result, *rec)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return &service.ListResult[service.RAGPage]{
		Data: result,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, nil
}

func (s *SQLite) GetRAGPage(ctx context.Context, id string) (*service.RAGPage, error) {
	sqlStr, _, err := s.goqu.From(s.tableRAGPages).
		Select(ragPageSelectColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get rag page query: %w", err)
	}

	row, err := scanRAGPageRow(s.db.QueryRowContext(ctx, sqlStr))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rag page %q: %w", id, err)
	}

	return ragPageRowToRecord(*row)
}

func (s *SQLite) GetRAGPageBySource(ctx context.Context, collectionID, source string) (*service.RAGPage, error) {
	sqlStr, _, err := s.goqu.From(s.tableRAGPages).
		Select(ragPageSelectColumns...).
		Where(
			goqu.I("collection_id").Eq(collectionID),
			goqu.I("source").Eq(source),
		).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get rag page by source query: %w", err)
	}

	row, err := scanRAGPageRow(s.db.QueryRowContext(ctx, sqlStr))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rag page by source %q: %w", source, err)
	}

	return ragPageRowToRecord(*row)
}

func (s *SQLite) UpsertRAGPage(ctx context.Context, page service.RAGPage) (*service.RAGPage, error) {
	metadataJSON, err := json.Marshal(page.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal rag page metadata: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)

	contentHash := page.ContentHash
	if contentHash == "" {
		h := sha256.Sum256([]byte(page.Content))
		contentHash = hex.EncodeToString(h[:])
	}

	id := page.ID
	if id == "" {
		id = ulid.Make().String()
	}

	// Check if existing page exists.
	existing, err := s.GetRAGPageBySource(ctx, page.CollectionID, page.Source)
	if err != nil {
		return nil, fmt.Errorf("check existing rag page: %w", err)
	}

	if existing != nil {
		// Update existing.
		sqlStr, _, err := s.goqu.Update(s.tableRAGPages).Set(
			goqu.Record{
				"path":         page.Path,
				"content":      page.Content,
				"content_type": page.ContentType,
				"metadata":     string(metadataJSON),
				"content_hash": contentHash,
				"updated_at":   now,
			},
		).Where(goqu.I("id").Eq(existing.ID)).ToSQL()
		if err != nil {
			return nil, fmt.Errorf("build update rag page query: %w", err)
		}

		if _, err := s.db.ExecContext(ctx, sqlStr); err != nil {
			return nil, fmt.Errorf("update rag page: %w", err)
		}

		return &service.RAGPage{
			ID:           existing.ID,
			CollectionID: page.CollectionID,
			Source:       page.Source,
			Path:         page.Path,
			Content:      page.Content,
			ContentType:  page.ContentType,
			Metadata:     page.Metadata,
			ContentHash:  contentHash,
			CreatedAt:    existing.CreatedAt,
			UpdatedAt:    now,
		}, nil
	}

	// Insert new.
	sqlStr, _, err := s.goqu.Insert(s.tableRAGPages).Rows(
		goqu.Record{
			"id":            id,
			"collection_id": page.CollectionID,
			"source":        page.Source,
			"path":          page.Path,
			"content":       page.Content,
			"content_type":  page.ContentType,
			"metadata":      string(metadataJSON),
			"content_hash":  contentHash,
			"created_at":    now,
			"updated_at":    now,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert rag page query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, sqlStr); err != nil {
		return nil, fmt.Errorf("insert rag page: %w", err)
	}

	return &service.RAGPage{
		ID:           id,
		CollectionID: page.CollectionID,
		Source:       page.Source,
		Path:         page.Path,
		Content:      page.Content,
		ContentType:  page.ContentType,
		Metadata:     page.Metadata,
		ContentHash:  contentHash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func (s *SQLite) DeleteRAGPage(ctx context.Context, id string) error {
	sqlStr, _, err := s.goqu.Delete(s.tableRAGPages).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete rag page query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, sqlStr)
	if err != nil {
		return fmt.Errorf("delete rag page %q: %w", id, err)
	}

	return nil
}

func (s *SQLite) DeleteRAGPagesByCollectionID(ctx context.Context, collectionID string) error {
	sqlStr, _, err := s.goqu.Delete(s.tableRAGPages).
		Where(goqu.I("collection_id").Eq(collectionID)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete rag pages by collection query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, sqlStr)
	if err != nil {
		return fmt.Errorf("delete rag pages for collection %q: %w", collectionID, err)
	}

	return nil
}

func (s *SQLite) DeleteRAGPageBySource(ctx context.Context, collectionID, source string) error {
	sqlStr, _, err := s.goqu.Delete(s.tableRAGPages).
		Where(
			goqu.I("collection_id").Eq(collectionID),
			goqu.I("source").Eq(source),
		).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete rag page by source query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, sqlStr)
	if err != nil {
		return fmt.Errorf("delete rag page by source %q: %w", source, err)
	}

	return nil
}
