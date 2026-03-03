package sqlite3

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/doug-martin/goqu/v9"
	"github.com/oklog/ulid/v2"
	"github.com/rakunlabs/at/internal/service"
	"github.com/rakunlabs/query"
)

type ragCollectionRow struct {
	ID                string         `db:"id"`
	Name              string         `db:"name"`
	Description       sql.NullString `db:"description"`
	VectorStoreConfig sql.NullString `db:"vector_store_config"`
	EmbeddingProvider string         `db:"embedding_provider"`
	EmbeddingModel    string         `db:"embedding_model"`
	EmbeddingURL      sql.NullString `db:"embedding_url"`
	EmbeddingAPIType  sql.NullString `db:"embedding_api_type"`
	ChunkSize         int            `db:"chunk_size"`
	ChunkOverlap      int            `db:"chunk_overlap"`
	CreatedAt         string         `db:"created_at"`
	UpdatedAt         string         `db:"updated_at"`
	CreatedBy         sql.NullString `db:"created_by"`
	UpdatedBy         sql.NullString `db:"updated_by"`
}

func (s *SQLite) ListRAGCollections(ctx context.Context, q *query.Query) (*service.ListResult[service.RAGCollection], error) {
	sql, total, err := s.buildListQuery(ctx, s.tableRAGCollections, q, "id", "name", "description", "vector_store_config", "embedding_provider", "embedding_model", "embedding_url", "embedding_api_type", "chunk_size", "chunk_overlap", "created_at", "updated_at", "created_by", "updated_by")
	if err != nil {
		return nil, fmt.Errorf("build list rag collections query: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("list rag collections: %w", err)
	}
	defer rows.Close()

	var items []service.RAGCollection
	for rows.Next() {
		var row ragCollectionRow
		if err := rows.Scan(&row.ID, &row.Name, &row.Description, &row.VectorStoreConfig, &row.EmbeddingProvider, &row.EmbeddingModel, &row.EmbeddingURL, &row.EmbeddingAPIType, &row.ChunkSize, &row.ChunkOverlap, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan rag collection row: %w", err)
		}

		rec, err := ragCollectionRowToRecord(row)
		if err != nil {
			return nil, err
		}
		items = append(items, *rec)
	}

	offset, limit := getPagination(q)

	return &service.ListResult[service.RAGCollection]{
		Data: items,
		Meta: service.ListMeta{
			Total:  total,
			Offset: offset,
			Limit:  limit,
		},
	}, rows.Err()
}

func (s *SQLite) GetRAGCollection(ctx context.Context, id string) (*service.RAGCollection, error) {
	query, _, err := s.goqu.From(s.tableRAGCollections).
		Select("id", "name", "description", "vector_store_config", "embedding_provider", "embedding_model", "embedding_url", "embedding_api_type", "chunk_size", "chunk_overlap", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get rag collection query: %w", err)
	}

	var row ragCollectionRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.VectorStoreConfig, &row.EmbeddingProvider, &row.EmbeddingModel, &row.EmbeddingURL, &row.EmbeddingAPIType, &row.ChunkSize, &row.ChunkOverlap, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rag collection %q: %w", id, err)
	}

	return ragCollectionRowToRecord(row)
}

func (s *SQLite) GetRAGCollectionByName(ctx context.Context, name string) (*service.RAGCollection, error) {
	query, _, err := s.goqu.From(s.tableRAGCollections).
		Select("id", "name", "description", "vector_store_config", "embedding_provider", "embedding_model", "embedding_url", "embedding_api_type", "chunk_size", "chunk_overlap", "created_at", "updated_at", "created_by", "updated_by").
		Where(goqu.I("name").Eq(name)).
		ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build get rag collection by name query: %w", err)
	}

	var row ragCollectionRow
	err = s.db.QueryRowContext(ctx, query).Scan(&row.ID, &row.Name, &row.Description, &row.VectorStoreConfig, &row.EmbeddingProvider, &row.EmbeddingModel, &row.EmbeddingURL, &row.EmbeddingAPIType, &row.ChunkSize, &row.ChunkOverlap, &row.CreatedAt, &row.UpdatedAt, &row.CreatedBy, &row.UpdatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get rag collection by name %q: %w", name, err)
	}

	return ragCollectionRowToRecord(row)
}

func (s *SQLite) CreateRAGCollection(ctx context.Context, c service.RAGCollection) (*service.RAGCollection, error) {
	vsConfigJSON, err := json.Marshal(c.VectorStore)
	if err != nil {
		return nil, fmt.Errorf("marshal vector store config: %w", err)
	}

	id := ulid.Make().String()
	now := time.Now().UTC()

	// Default chunk settings.
	chunkSize := c.ChunkSize
	if chunkSize <= 0 {
		chunkSize = 1000
	}
	chunkOverlap := c.ChunkOverlap
	if chunkOverlap < 0 {
		chunkOverlap = 200
	}

	query, _, err := s.goqu.Insert(s.tableRAGCollections).Rows(
		goqu.Record{
			"id":                  id,
			"name":                c.Name,
			"description":         c.Description,
			"vector_store_config": string(vsConfigJSON),
			"embedding_provider":  c.EmbeddingProvider,
			"embedding_model":     c.EmbeddingModel,
			"embedding_url":       c.EmbeddingURL,
			"embedding_api_type":  c.EmbeddingAPIType,
			"chunk_size":          chunkSize,
			"chunk_overlap":       chunkOverlap,
			"created_at":          now.Format(time.RFC3339),
			"updated_at":          now.Format(time.RFC3339),
			"created_by":          c.CreatedBy,
			"updated_by":          c.UpdatedBy,
		},
	).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build insert rag collection query: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, query); err != nil {
		return nil, fmt.Errorf("create rag collection %q: %w", c.Name, err)
	}

	return &service.RAGCollection{
		ID:                id,
		Name:              c.Name,
		Description:       c.Description,
		VectorStore:       c.VectorStore,
		EmbeddingProvider: c.EmbeddingProvider,
		EmbeddingModel:    c.EmbeddingModel,
		EmbeddingURL:      c.EmbeddingURL,
		EmbeddingAPIType:  c.EmbeddingAPIType,
		ChunkSize:         chunkSize,
		ChunkOverlap:      chunkOverlap,
		CreatedAt:         now.Format(time.RFC3339),
		UpdatedAt:         now.Format(time.RFC3339),
		CreatedBy:         c.CreatedBy,
		UpdatedBy:         c.UpdatedBy,
	}, nil
}

func (s *SQLite) UpdateRAGCollection(ctx context.Context, id string, c service.RAGCollection) (*service.RAGCollection, error) {
	vsConfigJSON, err := json.Marshal(c.VectorStore)
	if err != nil {
		return nil, fmt.Errorf("marshal vector store config: %w", err)
	}

	now := time.Now().UTC()

	query, _, err := s.goqu.Update(s.tableRAGCollections).Set(
		goqu.Record{
			"name":                c.Name,
			"description":         c.Description,
			"vector_store_config": string(vsConfigJSON),
			"embedding_provider":  c.EmbeddingProvider,
			"embedding_model":     c.EmbeddingModel,
			"embedding_url":       c.EmbeddingURL,
			"embedding_api_type":  c.EmbeddingAPIType,
			"chunk_size":          c.ChunkSize,
			"chunk_overlap":       c.ChunkOverlap,
			"updated_at":          now.Format(time.RFC3339),
			"updated_by":          c.UpdatedBy,
		},
	).Where(goqu.I("id").Eq(id)).ToSQL()
	if err != nil {
		return nil, fmt.Errorf("build update rag collection query: %w", err)
	}

	res, err := s.db.ExecContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("update rag collection %q: %w", id, err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return nil, fmt.Errorf("rows affected: %w", err)
	}
	if affected == 0 {
		return nil, nil
	}

	return s.GetRAGCollection(ctx, id)
}

func (s *SQLite) DeleteRAGCollection(ctx context.Context, id string) error {
	query, _, err := s.goqu.Delete(s.tableRAGCollections).
		Where(goqu.I("id").Eq(id)).
		ToSQL()
	if err != nil {
		return fmt.Errorf("build delete rag collection query: %w", err)
	}

	_, err = s.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("delete rag collection %q: %w", id, err)
	}

	return nil
}

func ragCollectionRowToRecord(row ragCollectionRow) (*service.RAGCollection, error) {
	var vsConfig service.RAGVectorStoreConfig
	if row.VectorStoreConfig.Valid && row.VectorStoreConfig.String != "" {
		if err := json.Unmarshal([]byte(row.VectorStoreConfig.String), &vsConfig); err != nil {
			return nil, fmt.Errorf("unmarshal vector store config for %q: %w", row.ID, err)
		}
	}

	return &service.RAGCollection{
		ID:                row.ID,
		Name:              row.Name,
		Description:       row.Description.String,
		VectorStore:       vsConfig,
		EmbeddingProvider: row.EmbeddingProvider,
		EmbeddingModel:    row.EmbeddingModel,
		EmbeddingURL:      row.EmbeddingURL.String,
		EmbeddingAPIType:  row.EmbeddingAPIType.String,
		ChunkSize:         row.ChunkSize,
		ChunkOverlap:      row.ChunkOverlap,
		CreatedAt:         row.CreatedAt,
		UpdatedAt:         row.UpdatedAt,
		CreatedBy:         row.CreatedBy.String,
		UpdatedBy:         row.UpdatedBy.String,
	}, nil
}
