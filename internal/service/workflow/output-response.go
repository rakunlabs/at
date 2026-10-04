package workflow

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/rakunlabs/at/internal/service"
)

// Output response modes. They only change how a synchronous HTTP caller
// (webhook or run API with ?sync=true) receives the result; workflow outputs
// themselves are always the same JSON map.
const (
	OutputResponseJSON      = "json"
	OutputResponseFile      = "file"
	OutputResponseMultipart = "multipart"
)

// ValidOutputResponseMode reports whether mode is a known response mode.
func ValidOutputResponseMode(mode string) bool {
	switch mode {
	case OutputResponseJSON, OutputResponseFile, OutputResponseMultipart:
		return true
	}
	return false
}

// OutputResponse describes how an Output node wants the synchronous HTTP
// response rendered. Nil means the default JSON envelope.
type OutputResponse struct {
	Mode        string
	Files       []ResponseFile
	Disposition string // "attachment" (default) or "inline"
	// open reads a stored file under the execution identity of the node that
	// resolved it. It is detached from the run's cancellation, because the
	// HTTP handler streams after the Output node returned and the rest of the
	// graph may already have finished.
	open func(workspacePath string) (*os.Root, *os.File, error)
}

// ResponseFile is one file of the response: either a stored file addressed
// relative to the execution root, or content an upstream node supplied
// inline (Script's {name, content_base64}).
type ResponseFile struct {
	WorkspacePath string
	Name          string
	ContentType   string
	Content       []byte
	HasContent    bool
}

// NewOutputResponse builds a response whose stored files are opened through
// the execution root of ctx, so containment and files.read admission match
// the node that produced the response.
func NewOutputResponse(ctx context.Context, mode, disposition string, files []ResponseFile) *OutputResponse {
	detached := context.WithoutCancel(ctx)
	return &OutputResponse{
		Mode:        mode,
		Files:       files,
		Disposition: disposition,
		open: func(workspacePath string) (*os.Root, *os.File, error) {
			root, name, err := service.OpenExecutionRoot(detached, workspacePath, false)
			if err != nil {
				return nil, nil, err
			}
			f, err := root.Open(name)
			if err != nil {
				_ = root.Close()
				return nil, nil, err
			}
			return root, f, nil
		},
	}
}

// OpenedFile is a response file ready to stream.
type OpenedFile struct {
	ResponseFile
	Size   int64
	Reader io.Reader
	close  func()
}

// Close releases the file handle.
func (f *OpenedFile) Close() {
	if f.close != nil {
		f.close()
	}
}

// OpenFiles opens every file of the response. It fails (and releases what
// it opened) when any file is missing, so a caller can still answer with an
// error before writing headers.
func (r *OutputResponse) OpenFiles() ([]*OpenedFile, error) {
	opened := make([]*OpenedFile, 0, len(r.Files))
	fail := func(err error) ([]*OpenedFile, error) {
		for _, f := range opened {
			f.Close()
		}
		return nil, err
	}
	for _, file := range r.Files {
		if file.HasContent {
			opened = append(opened, &OpenedFile{ResponseFile: file, Size: int64(len(file.Content)), Reader: bytes.NewReader(file.Content)})
			continue
		}
		if r.open == nil {
			return fail(fmt.Errorf("file %q: no execution root", file.Name))
		}
		root, f, err := r.open(file.WorkspacePath)
		if err != nil {
			return fail(fmt.Errorf("file %q: %w", file.Name, err))
		}
		info, err := f.Stat()
		if err == nil && info.IsDir() {
			err = fmt.Errorf("is a directory")
		}
		if err != nil {
			_ = f.Close()
			_ = root.Close()
			return fail(fmt.Errorf("file %q: %w", file.Name, err))
		}
		opened = append(opened, &OpenedFile{
			ResponseFile: file,
			Size:         info.Size(),
			Reader:       f,
			close:        func() { _ = f.Close(); _ = root.Close() },
		})
	}
	return opened, nil
}

// responseSlot is shared by a registry and its fan-out branches, so an
// Output node inside a Loop still reaches the run's caller. The first
// response wins, matching the first-output-wins early signal.
type responseSlot struct {
	mu       sync.Mutex
	response *OutputResponse
}

// SetResponse records the HTTP response shape requested by an Output node.
// Only the first call has an effect.
func (r *Registry) SetResponse(resp *OutputResponse) {
	if r == nil || r.response == nil || resp == nil {
		return
	}
	r.response.mu.Lock()
	if r.response.response == nil {
		r.response.response = resp
	}
	r.response.mu.Unlock()
}

// Response returns the response shape requested by an Output node, if any.
func (r *Registry) Response() *OutputResponse {
	if r == nil || r.response == nil {
		return nil
	}
	r.response.mu.Lock()
	defer r.response.mu.Unlock()
	return r.response.response
}

// FileRefLimit bounds how many file references are collected from one value.
const FileRefLimit = 20

// IsFileRef reports whether v is a run file reference
// ({workspace_path|path, name, content_type, ...}) or an inline file
// ({name, content_base64}).
func IsFileRef(v any) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	name, _ := m["name"].(string)
	if name == "" {
		return false
	}
	if encoded, ok := m["content_base64"].(string); ok && encoded != "" {
		return true
	}
	wp, _ := m["workspace_path"].(string)
	p, _ := m["path"].(string)
	_, hasType := m["content_type"].(string)
	return (strings.TrimSpace(wp) != "" || strings.TrimSpace(p) != "") && hasType
}

// CollectFileRefs walks v (maps in sorted key order, lists in order) and
// returns every file reference found, without descending into one. The walk
// is bounded in depth and count because outputs are arbitrary JSON.
func CollectFileRefs(v any, limit int) []map[string]any {
	var out []map[string]any
	var walk func(v any, depth int)
	walk = func(v any, depth int) {
		if depth > 16 || len(out) >= limit {
			return
		}
		switch t := v.(type) {
		case map[string]any:
			if IsFileRef(t) {
				out = append(out, t)
				return
			}
			keys := make([]string, 0, len(t))
			for k := range t {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(t[k], depth+1)
			}
		case []any:
			for _, item := range t {
				walk(item, depth+1)
			}
		case []map[string]any:
			for _, item := range t {
				walk(item, depth+1)
			}
		}
	}
	walk(v, 0)
	return out
}
