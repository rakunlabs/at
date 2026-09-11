package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRecoveryTicketFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ticket")
	if err := writeRecoveryTicketFile(path, func() (string, error) { return "private-ticket", nil }); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v", st.Mode())
	}
	called := false
	if err := writeRecoveryTicketFile(path, func() (string, error) { called = true; return "replacement", nil }); err == nil || called {
		t.Fatal("existing output must be refused before issuance")
	}
	link := filepath.Join(dir, "symlink")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := writeRecoveryTicketFile(link, func() (string, error) { called = true; return "replacement", nil }); err == nil || called {
		t.Fatal("symlink output accepted")
	}
	failed := filepath.Join(dir, "failed")
	if err := writeRecoveryTicketFile(failed, func() (string, error) { return "", errors.New("unavailable") }); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Fatal("failed output not cleaned")
	}
}
