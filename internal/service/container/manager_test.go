package container

import (
	"strings"
	"testing"
)

func TestBoundedOutput(t *testing.T) {
	w := newBoundedOutput(4)
	if n, err := w.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("Write() = %d, %v", n, err)
	}
	if got := w.String(); !strings.HasPrefix(got, "abcd\n[output truncated") {
		t.Fatalf("String() = %q", got)
	}
	if n, err := w.Write([]byte("more")); err != nil || n != 4 {
		t.Fatalf("second Write() = %d, %v", n, err)
	}
	if got := w.String(); strings.Contains(got, "more") {
		t.Fatalf("bounded output retained overflow: %q", got)
	}
}
