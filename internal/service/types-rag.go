package service

import (
	"context"

	"github.com/rakunlabs/query"
	"github.com/worldline-go/types"
)

// ─── RAG Collections ───

// RAGGitSourceConfig holds the git repository configuration for automatic RAG ingestion.
type RAGGitSourceConfig struct {
	RepoURL        string `json:"repo_url"`
	Branch         string `json:"branch,omitempty"`
	FilePatterns   string `json:"file_patterns,omitempty"`
	TokenVariable  string `json:"token_variable,omitempty"`
	TokenUser      string `json:"token_user,omitempty"`
	SSHKeyVariable string `json:"ssh_key_variable,omitempty"`
	MaxFileSize    int    `json:"max_file_size,omitempty"`
}

// RAGCollectionConfig holds the configuration fields for a RAG collection.
type RAGCollectionConfig struct {
	Description         string               `json:"description,omitempty"`
	VectorStore         RAGVectorStoreConfig `json:"vector_store"`
	EmbeddingProvider   string               `json:"embedding_provider"`
	EmbeddingModel      string               `json:"embedding_model,omitempty"`
	EmbeddingURL        string               `json:"embedding_url,omitempty"`
	EmbeddingAPIType    string               `json:"embedding_api_type,omitempty"`
	EmbeddingBearerAuth bool                 `json:"embedding_bearer_auth,omitempty"`
	ChunkSize           int                  `json:"chunk_size"`
	ChunkOverlap        int                  `json:"chunk_overlap"`
	GitSource           *RAGGitSourceConfig  `json:"git_source,omitempty"`
}

// RAGCollection represents a named namespace for RAG documents.
type RAGCollection struct {
	ID        string              `json:"id"`
	Name      string              `json:"name"`
	Config    RAGCollectionConfig `json:"config"`
	CreatedAt string              `json:"created_at"`
	UpdatedAt string              `json:"updated_at"`
	CreatedBy string              `json:"created_by"`
	UpdatedBy string              `json:"updated_by"`
}

// RAGVectorStoreConfig holds the type and connection parameters for a vector store backend.
type RAGVectorStoreConfig struct {
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}

// RAGCollectionStorer defines CRUD operations for RAG collection configurations.
type RAGCollectionStorer interface {
	ListRAGCollections(ctx context.Context, q *query.Query) (*ListResult[RAGCollection], error)
	GetRAGCollection(ctx context.Context, id string) (*RAGCollection, error)
	GetRAGCollectionByName(ctx context.Context, name string) (*RAGCollection, error)
	CreateRAGCollection(ctx context.Context, c RAGCollection) (*RAGCollection, error)
	UpdateRAGCollection(ctx context.Context, id string, c RAGCollection) (*RAGCollection, error)
	DeleteRAGCollection(ctx context.Context, id string) error
}

// ─── RAG State ───

// RAGState represents the last processed state for a RAG source.
type RAGState struct {
	Key       string     `json:"key"`
	Value     string     `json:"value"`
	UpdatedAt types.Time `json:"updated_at"`
}

// RAGStateStorer defines CRUD operations for RAG states.
type RAGStateStorer interface {
	GetRAGState(ctx context.Context, key string) (*RAGState, error)
	SetRAGState(ctx context.Context, key string, value string) error
}

// ─── RAG Pages ───

// RAGPage represents the original (pre-chunked) content of a file stored in a RAG collection.
type RAGPage struct {
	ID           string         `json:"id"`
	CollectionID string         `json:"collection_id"`
	Source       string         `json:"source"`
	Path         string         `json:"path,omitempty"`
	Content      string         `json:"content"`
	ContentType  string         `json:"content_type,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	ContentHash  string         `json:"content_hash,omitempty"`
	CreatedAt    string         `json:"created_at"`
	UpdatedAt    string         `json:"updated_at"`
}

// RAGPageStorer defines CRUD operations for RAG page content.
type RAGPageStorer interface {
	ListRAGPages(ctx context.Context, collectionID string, q *query.Query) (*ListResult[RAGPage], error)
	GetRAGPage(ctx context.Context, id string) (*RAGPage, error)
	GetRAGPageBySource(ctx context.Context, collectionID, source string) (*RAGPage, error)
	UpsertRAGPage(ctx context.Context, page RAGPage) (*RAGPage, error)
	DeleteRAGPage(ctx context.Context, id string) error
	DeleteRAGPagesByCollectionID(ctx context.Context, collectionID string) error
	DeleteRAGPageBySource(ctx context.Context, collectionID, source string) error
}
