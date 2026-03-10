package postgres

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
	"github.com/worldline-go/types"
)

// ─── RAG Page CRUD ───

type ragPageRow struct {
	ID           string        `db:"id"`
	CollectionID string        `db:"collection_id"`
	Source       string        `db:"source"`
	Path         string        `db:"path"`
	Content      string        `db:"content"`
	ContentType  string        `db:"content_type"`
	Metadata     types.RawJSON `db:"metadata"`
	ContentHash  string        `db:"content_hash"`
	CreatedAt    time.Time     `db:"created_at"`
	UpdatedAt    time.Time     `db:"updated_at"`
}

func ragPageRowToRecord(row ragPageRow) (*service.RAGPage, error) {
	var metadata map[string]any
	if len(row.Metadata) > 0 {
		if err := json.Unmarshal(row.Metadata, &metadata); err != nil {
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
		CreatedAt:    row.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    row.UpdatedAt.Format(time.RFC3339),
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

func (p *Postgres) ListRAGPages(ctx context.Context, collectionID string, q *query.Query) (*service.ListResult[service.RAGPage], error) {
	// We manually filter by collection_id and then apply pagination via the query.
	// Count total items for this collection.
	countSQL, _, err := p.goqu.From(p.tableRAGPages).
		Select(goqu.COUNT("*")).
		Where(goqu.I("collection_id").Eq(collectionID)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build count rag pages query: %w", err)
	}

	var total uint64
	if err := p.db.QueryRowContext(ctx, countSQL).Scan(&total); err != nil {
		return nil, fmt.Errorf("count rag pages: %w", err)
	}

	// Build data query.
	ds := p.goqu.From(p.tableRAGPages).
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

	rows, err := p.db.QueryContext(ctx, sqlStr)
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

func (p *Postgres) GetRAGPage(ctx context.Context, id string) (*service.RAGPage, error) {
	sqlStr, _, err := p.goqu.From(p.tableRAGPages).
		Select(ragPageSelectColumns...).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get rag page query: %w", err)
	}

	row, err := scanRAGPageRow(p.db.QueryRowContext(ctx, sqlStr))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rag page %q: %w", id, err)
	}

	return ragPageRowToRecord(*row)
}

func (p *Postgres) GetRAGPageBySource(ctx context.Context, collectionID, source string) (*service.RAGPage, error) {
	sqlStr, _, err := p.goqu.From(p.tableRAGPages).
		Select(ragPageSelectColumns...).
		Where(
			goqu.I("collection_id").Eq(collectionID),
			goqu.I("source").Eq(source),
		).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get rag page by source query: %w", err)
	}

	row, err := scanRAGPageRow(p.db.QueryRowContext(ctx, sqlStr))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rag page by source %q: %w", source, err)
	}

	return ragPageRowToRecord(*row)
}

func (p *Postgres) UpsertRAGPage(ctx context.Context, page service.RAGPage) (*service.RAGPage, error) {
	metadataJSON, err := json.Marshal(page.Metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal rag page metadata: %w", err)
	}

	now := time.Now().UTC()

	// Compute content hash if not provided.
	contentHash := page.ContentHash
	if contentHash == "" {
		h := sha256.Sum256([]byte(page.Content))
		contentHash = hex.EncodeToString(h[:])
	}

	id := page.ID
	if id == "" {
		id = ulid.Make().String()
	}

	// Use INSERT ... ON CONFLICT to upsert.
	sqlStr := fmt.Sprintf(
		`INSERT INTO %s (id, collection_id, source, path, content, content_type, metadata, content_hash, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT (collection_id, source) DO UPDATE SET
		   path = EXCLUDED.path,
		   content = EXCLUDED.content,
		   content_type = EXCLUDED.content_type,
		   metadata = EXCLUDED.metadata,
		   content_hash = EXCLUDED.content_hash,
		   updated_at = EXCLUDED.updated_at
		 RETURNING id, created_at, updated_at`,
		p.tableRAGPages.GetTable(),
	)

	var retID string
	var retCreatedAt, retUpdatedAt time.Time
	err = p.db.QueryRowContext(ctx, sqlStr,
		id, page.CollectionID, page.Source, page.Path, page.Content,
		page.ContentType, types.RawJSON(metadataJSON), contentHash, now, now,
	).Scan(&retID, &retCreatedAt, &retUpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("upsert rag page: %w", err)
	}

	return &service.RAGPage{
		ID:           retID,
		CollectionID: page.CollectionID,
		Source:       page.Source,
		Path:         page.Path,
		Content:      page.Content,
		ContentType:  page.ContentType,
		Metadata:     page.Metadata,
		ContentHash:  contentHash,
		CreatedAt:    retCreatedAt.Format(time.RFC3339),
		UpdatedAt:    retUpdatedAt.Format(time.RFC3339),
	}, nil
}

func (p *Postgres) DeleteRAGPage(ctx context.Context, id string) error {
	sqlStr, _, err := p.goqu.Delete(p.tableRAGPages).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete rag page query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, sqlStr)
	if err != nil {
		return fmt.Errorf("delete rag page %q: %w", id, err)
	}

	return nil
}

func (p *Postgres) DeleteRAGPagesByCollectionID(ctx context.Context, collectionID string) error {
	sqlStr, _, err := p.goqu.Delete(p.tableRAGPages).
		Where(goqu.I("collection_id").Eq(collectionID)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete rag pages by collection query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, sqlStr)
	if err != nil {
		return fmt.Errorf("delete rag pages for collection %q: %w", collectionID, err)
	}

	return nil
}

func (p *Postgres) DeleteRAGPageBySource(ctx context.Context, collectionID, source string) error {
	sqlStr, _, err := p.goqu.Delete(p.tableRAGPages).
		Where(
			goqu.I("collection_id").Eq(collectionID),
			goqu.I("source").Eq(source),
		).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete rag page by source query: %w", err)
	}

	_, err = p.db.ExecContext(ctx, sqlStr)
	if err != nil {
		return fmt.Errorf("delete rag page by source %q: %w", source, err)
	}

	return nil
}
