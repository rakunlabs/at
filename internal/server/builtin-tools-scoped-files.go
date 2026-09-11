package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

func (s *Server) execScopedFileRead(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["file_path"].(string)
	if path == "" {
		return "", fmt.Errorf("file_path is required")
	}
	root, name, err := service.OpenExecutionRoot(ctx, path, false)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := service.OpenExecutionFile(root, name, os.O_RDONLY, 0)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return s.execScopedFileList(ctx, map[string]any{"path": path})
	}
	if !info.Mode().IsRegular() {
		return "", service.ErrExecutionDenied
	}
	offset, limit := 1, 2000
	if n, ok := args["offset"].(float64); ok && n > 0 {
		offset = int(n)
	}
	if n, ok := args["limit"].(float64); ok && n > 0 {
		limit = min(int(n), 2000)
	}
	scanner := bufio.NewScanner(io.LimitReader(f, 8<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var out strings.Builder
	for line := 1; scanner.Scan(); line++ {
		if line < offset {
			continue
		}
		if line >= offset+limit || out.Len() >= 65536 {
			break
		}
		fmt.Fprintf(&out, "%d: %s\n", line, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	return out.String(), nil
}

func (s *Server) execScopedFileWrite(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["file_path"].(string)
	content, ok := args["content"].(string)
	if path == "" || !ok {
		return "", fmt.Errorf("file_path and content are required")
	}
	root, name, err := service.OpenExecutionRoot(ctx, path, true)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := service.MkdirExecutionAll(root, filepath.Dir(name), 0700); err != nil {
		return "", fmt.Errorf("create directory: %w", err)
	}
	_, err = service.ReplaceExecutionFile(root, name, strings.NewReader(content))
	if err != nil {
		return "", fmt.Errorf("write file: %w", err)
	}
	return "Wrote " + name, nil
}

func (s *Server) execScopedFileList(ctx context.Context, args map[string]any) (string, error) {
	path, _ := args["path"].(string)
	root, name, err := service.OpenExecutionRoot(ctx, path, false)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := service.OpenExecutionFile(root, name, os.O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	entries, err := f.ReadDir(2000)
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("list directory: %w", err)
	}
	result := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		path := filepath.Join(name, e.Name())
		if service.CheckExecution(ctx, service.ExecutionAction{Kind: "file", Name: "files.read", Path: path}) != nil {
			continue
		}
		result = append(result, path)
	}
	b, err := json.Marshal(result)
	return string(b), err
}
