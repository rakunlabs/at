package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/tmc/langchaingo/documentloaders"
	"github.com/tmc/langchaingo/schema"
	"github.com/tmc/langchaingo/textsplitter"
)

// SupportedContentTypes returns the list of MIME types the loader supports.
func SupportedContentTypes() []string {
	return []string{
		"text/plain",
		"text/markdown",
		"text/csv",
		"text/html",
		"application/pdf",
		"application/json",
	}
}

// LoadDocuments reads raw content and returns split document chunks ready for embedding.
// The contentType should be a MIME type (e.g. "text/markdown").
// The source string is added to each document's metadata as "source".
// Any entries in extraMetadata are merged into every chunk's metadata (e.g. repo_url, commit_sha).
func LoadDocuments(ctx context.Context, content io.Reader, contentType string, source string, chunkSize, chunkOverlap int, extraMetadata map[string]any) ([]schema.Document, error) {
	if chunkSize <= 0 {
		chunkSize = 512
	}
	if chunkOverlap < 0 {
		chunkOverlap = 100
	}

	// Normalise content type — strip parameters (e.g. "text/plain; charset=utf-8" → "text/plain").
	if idx := strings.IndexByte(contentType, ';'); idx >= 0 {
		contentType = strings.TrimSpace(contentType[:idx])
	}
	contentType = strings.ToLower(contentType)

	// Load raw documents and get an appropriate splitter.
	docs, splitter, err := loadRaw(ctx, content, contentType, chunkSize, chunkOverlap)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", contentType, err)
	}

	// Split documents into chunks.
	chunks, err := textsplitter.SplitDocuments(splitter, docs)
	if err != nil {
		return nil, fmt.Errorf("split documents: %w", err)
	}

	// Inject source metadata into every chunk.
	for i := range chunks {
		if chunks[i].Metadata == nil {
			chunks[i].Metadata = make(map[string]any)
		}
		chunks[i].Metadata["source"] = source
		chunks[i].Metadata["content_type"] = contentType
		// Merge extra metadata (e.g. repo_url, commit_sha).
		for k, v := range extraMetadata {
			chunks[i].Metadata[k] = v
		}
	}

	return chunks, nil
}

// LoadDocumentsFromBytes is a convenience wrapper around LoadDocuments that
// accepts a byte slice instead of an io.Reader.
func LoadDocumentsFromBytes(ctx context.Context, data []byte, contentType string, source string, chunkSize, chunkOverlap int, extraMetadata map[string]any) ([]schema.Document, error) {
	return LoadDocuments(ctx, bytes.NewReader(data), contentType, source, chunkSize, chunkOverlap, extraMetadata)
}

// loadRaw dispatches to the appropriate langchaingo loader based on content type
// and returns the raw (unsplit) documents along with an appropriate splitter.
func loadRaw(ctx context.Context, r io.Reader, contentType string, chunkSize, chunkOverlap int) ([]schema.Document, textsplitter.TextSplitter, error) {
	switch contentType {
	case "text/plain":
		loader := documentloaders.NewText(r)
		docs, err := loader.Load(ctx)
		if err != nil {
			return nil, nil, err
		}
		splitter := textsplitter.NewRecursiveCharacter(
			textsplitter.WithChunkSize(chunkSize),
			textsplitter.WithChunkOverlap(chunkOverlap),
		)
		return docs, splitter, nil

	case "text/markdown":
		// Read all content, then use the markdown-aware splitter directly.
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, nil, fmt.Errorf("read markdown: %w", err)
		}
		splitter := textsplitter.NewMarkdownTextSplitter(
			textsplitter.WithChunkSize(chunkSize),
			textsplitter.WithChunkOverlap(chunkOverlap),
			textsplitter.WithHeadingHierarchy(true),
		)
		docs := []schema.Document{{PageContent: string(data)}}
		return docs, splitter, nil

	case "text/csv":
		loader := documentloaders.NewCSV(r)
		docs, err := loader.Load(ctx)
		if err != nil {
			return nil, nil, err
		}
		splitter := textsplitter.NewRecursiveCharacter(
			textsplitter.WithChunkSize(chunkSize),
			textsplitter.WithChunkOverlap(chunkOverlap),
		)
		return docs, splitter, nil

	case "text/html":
		loader := documentloaders.NewHTML(r)
		docs, err := loader.Load(ctx)
		if err != nil {
			return nil, nil, err
		}
		splitter := textsplitter.NewRecursiveCharacter(
			textsplitter.WithChunkSize(chunkSize),
			textsplitter.WithChunkOverlap(chunkOverlap),
		)
		return docs, splitter, nil

	case "application/pdf":
		// PDF loader needs io.ReaderAt + size. Read all into memory.
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, nil, fmt.Errorf("read pdf: %w", err)
		}
		reader := bytes.NewReader(data)
		loader := documentloaders.NewPDF(reader, int64(len(data)))
		docs, err := loader.Load(ctx)
		if err != nil {
			return nil, nil, err
		}
		splitter := textsplitter.NewRecursiveCharacter(
			textsplitter.WithChunkSize(chunkSize),
			textsplitter.WithChunkOverlap(chunkOverlap),
		)
		return docs, splitter, nil

	case "application/json":
		// Parse JSON and convert to text. Each top-level array element
		// becomes a document; an object becomes a single document.
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, nil, fmt.Errorf("read json: %w", err)
		}
		docs, err := loadJSON(data)
		if err != nil {
			return nil, nil, err
		}
		splitter := textsplitter.NewRecursiveCharacter(
			textsplitter.WithChunkSize(chunkSize),
			textsplitter.WithChunkOverlap(chunkOverlap),
		)
		return docs, splitter, nil

	default:
		return nil, nil, fmt.Errorf("unsupported content type: %q", contentType)
	}
}

// loadJSON converts JSON data into documents. Arrays produce one doc per element;
// objects produce a single document with the pretty-printed JSON as content.
func loadJSON(data []byte) ([]schema.Document, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, nil
	}

	// Try as array first.
	if data[0] == '[' {
		var arr []json.RawMessage
		if err := json.Unmarshal(data, &arr); err != nil {
			return nil, fmt.Errorf("parse json array: %w", err)
		}
		docs := make([]schema.Document, 0, len(arr))
		for i, elem := range arr {
			pretty, _ := prettyJSON(elem)
			docs = append(docs, schema.Document{
				PageContent: pretty,
				Metadata:    map[string]any{"index": i},
			})
		}
		return docs, nil
	}

	// Single object or value.
	pretty, err := prettyJSON(data)
	if err != nil {
		// Not valid JSON — treat as raw text.
		return []schema.Document{{PageContent: string(data)}}, nil
	}

	return []schema.Document{{PageContent: pretty}}, nil
}

// prettyJSON formats JSON data with indentation for readability.
func prettyJSON(data []byte) (string, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// DetectContentType returns a MIME type based on file extension.
// Returns empty string if the extension is not recognized.
func DetectContentType(filename string) string {
	ext := strings.ToLower(filename)
	if idx := strings.LastIndexByte(ext, '.'); idx >= 0 {
		ext = ext[idx:]
	} else {
		return ""
	}

	switch ext {
	case ".txt":
		return "text/plain"
	case ".md", ".markdown":
		return "text/markdown"
	case ".csv":
		return "text/csv"
	case ".html", ".htm":
		return "text/html"
	case ".pdf":
		return "application/pdf"
	case ".json":
		return "application/json"
	default:
		return ""
	}
}
