package blob

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rakunlabs/at/internal/service"
)

func TestFilesystemDirectoryListsExternalFilesAndRejectsAliases(t *testing.T) {
	root := t.TempDir()
	prefix := "workspaces/one/files/"
	if err := os.MkdirAll(filepath.Join(root, prefix, "docs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, prefix, "external.txt"), []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, prefix, "alias")); err != nil {
		t.Fatal(err)
	}
	s, err := newFilesystem(service.MediaFilesystemSettings{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := s.ListDirectory(t.Context(), prefix)
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v, %v", entries, err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Key, prefix) {
			t.Fatal(entry.Key)
		}
	}
	if _, err := s.ListDirectory(t.Context(), prefix+"alias/"); err == nil {
		t.Fatal("listed symlink")
	}
	if _, err := s.ListDirectory(t.Context(), "../"); err == nil {
		t.Fatal("accepted traversal")
	}
	entries, err = s.ListDirectory(t.Context(), "workspaces/missing/files/")
	if err != nil || len(entries) != 0 {
		t.Fatalf("missing root: %+v, %v", entries, err)
	}
}

func TestS3DirectoryPaginationAndPrefixIsolation(t *testing.T) {
	prefix := "my prefix/workspaces/one/files/"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		if r.URL.Path != "/media" || q.Get("prefix") != prefix || q.Get("delimiter") != "/" || q.Get("list-type") != "2" || r.Header.Get("Authorization") == "" {
			t.Errorf("request: %s", r.URL)
		}
		if strings.Contains(r.URL.RawQuery, "+") {
			t.Error("noncanonical query encoding")
		}
		if calls == 1 {
			fmt.Fprintf(w, `<ListBucketResult><IsTruncated>true</IsTruncated><NextContinuationToken>next +</NextContinuationToken><Contents><Key>%sexternal.txt</Key><Size>8</Size><LastModified>2026-10-07T12:00:00Z</LastModified></Contents><Contents><Key>other/secret</Key></Contents><CommonPrefixes><Prefix>%sdocs/</Prefix></CommonPrefixes></ListBucketResult>`, prefix, prefix)
		} else {
			if q.Get("continuation-token") != "next +" {
				t.Error("lost continuation token")
			}
			fmt.Fprintf(w, `<ListBucketResult><IsTruncated>false</IsTruncated><Contents><Key>%ssecond.txt</Key><Size>4</Size></Contents></ListBucketResult>`, prefix)
		}
	}))
	defer server.Close()
	entries, err := newS3Store(t, server.URL, true).ListDirectory(t.Context(), prefix)
	if err != nil || len(entries) != 3 || calls != 2 {
		t.Fatalf("entries = %+v, calls = %d, err = %v", entries, calls, err)
	}
}
