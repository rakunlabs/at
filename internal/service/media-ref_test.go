package service

import "testing"

func TestMediaRefMarkdown(t *testing.T) {
	if got := MediaRefMarkdown("01ABC", "a [b].png", true); got != "![a b.png](media:01ABC)" {
		t.Fatalf("image = %q", got)
	}
	if got := MediaRefMarkdown("01ABC", "report.pdf", false); got != "[report.pdf](media:01ABC)" {
		t.Fatalf("link = %q", got)
	}
}

func TestReplaceMediaRefs(t *testing.T) {
	text := "![a](media:OLD) [b](<media:OLD>) ![c](media:OTHER) media:OLD"
	got := ReplaceMediaRefs(text, map[string]string{"OLD": "NEW"})
	want := "![a](media:NEW) [b](<media:NEW>) ![c](media:OTHER) media:OLD"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
