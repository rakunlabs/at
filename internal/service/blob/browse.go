package blob

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DirectoryLister reads immediate children from the backend, not a DB catalog.
// Prefix must end in a slash; returned keys retain that exact prefix.
type DirectoryLister interface {
	ListDirectory(context.Context, string) ([]Entry, error)
}

type Entry struct {
	Key      string
	IsDir    bool
	Size     int64
	Modified time.Time
}

const directoryLimit = 10000

func validateDirectoryPrefix(prefix string) error {
	if !strings.HasSuffix(prefix, "/") {
		return errors.New("directory prefix must end in a slash")
	}
	return ValidateKey(strings.TrimSuffix(prefix, "/"))
}

// Storage namespaces must not be aliases into another workspace's tree.
func rejectStorageSymlinks(root *os.Root, key string) error {
	parts := strings.Split(key, "/")
	for i := range parts {
		info, err := root.Lstat(filepath.FromSlash(strings.Join(parts[:i+1], "/")))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("storage symlinks are not browsable")
		}
	}
	return nil
}

func (f *filesystemStore) ListDirectory(ctx context.Context, prefix string) ([]Entry, error) {
	if err := validateDirectoryPrefix(prefix); err != nil {
		return nil, err
	}
	root, err := f.open()
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := rejectStorageSymlinks(root, strings.TrimSuffix(prefix, "/")); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []Entry{}, nil
		}
		return nil, err
	}
	dir, err := root.Open(filepath.FromSlash(strings.TrimSuffix(prefix, "/")))
	if errors.Is(err, fs.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open storage directory: %w", err)
	}
	defer dir.Close()
	entries := make([]Entry, 0)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := dir.ReadDir(256)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("read storage directory: %w", err)
		}
		for _, item := range batch {
			// No aliases into other workspace trees, or temporary upload files.
			if item.Type()&os.ModeSymlink != 0 || strings.HasPrefix(item.Name(), filesystemProbePrefix) {
				continue
			}
			key := prefix + item.Name()
			if ValidateKey(key) != nil {
				continue
			}
			info, err := item.Info()
			if err != nil {
				return nil, fmt.Errorf("stat storage entry: %w", err)
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				continue
			}
			entries = append(entries, Entry{Key: key, IsDir: info.IsDir(), Size: info.Size(), Modified: info.ModTime()})
			if len(entries) > directoryLimit {
				return nil, errors.New("storage directory exceeds 10000 entries; use a narrower directory")
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return entries, nil
}

func (s *s3Store) ListDirectory(ctx context.Context, prefix string) ([]Entry, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := validateDirectoryPrefix(prefix); err != nil {
		return nil, err
	}
	entries := make([]Entry, 0)
	token := ""
	seen := make(map[string]bool)
	for {
		u := s.objectURL("")
		q := u.Query()
		q.Set("list-type", "2")
		q.Set("prefix", prefix)
		q.Set("delimiter", "/")
		q.Set("max-keys", "1000")
		if token != "" {
			q.Set("continuation-token", token)
		}
		u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
		resp, err := s.do(ctx, http.MethodGet, u, nil, "")
		if err != nil {
			return nil, err
		}
		var result struct {
			XMLName   xml.Name `xml:"ListBucketResult"`
			Truncated bool     `xml:"IsTruncated"`
			Next      string   `xml:"NextContinuationToken"`
			Contents  []struct {
				Key          string
				Size         int64
				LastModified time.Time
			} `xml:"Contents"`
			Prefixes []struct{ Prefix string } `xml:"CommonPrefixes"`
		}
		err = xml.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode S3 directory: %w", err)
		}
		for _, item := range result.Contents {
			name := strings.TrimPrefix(item.Key, prefix)
			if !strings.HasPrefix(item.Key, prefix) || name == "" || strings.Contains(name, "/") || ValidateKey(item.Key) != nil {
				continue
			}
			entries = append(entries, Entry{Key: item.Key, Size: item.Size, Modified: item.LastModified})
		}
		for _, item := range result.Prefixes {
			key := strings.TrimSuffix(item.Prefix, "/")
			name := strings.TrimPrefix(key, prefix)
			if !strings.HasPrefix(key, prefix) || name == "" || strings.Contains(name, "/") || ValidateKey(key) != nil {
				continue
			}
			entries = append(entries, Entry{Key: key, IsDir: true})
		}
		if len(entries) > directoryLimit {
			return nil, errors.New("storage directory exceeds 10000 entries; use a narrower directory")
		}
		if !result.Truncated {
			return entries, nil
		}
		if result.Next == "" || seen[result.Next] {
			return nil, errors.New("S3 returned invalid directory pagination")
		}
		seen[result.Next] = true
		token = result.Next
		if len(seen) > 100 {
			return nil, errors.New("S3 directory pagination limit exceeded")
		}
	}
}
