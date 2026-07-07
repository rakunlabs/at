package server

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseNewCommandArgs(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantTopic string
		wantMax   int
	}{
		{
			name:      "no flags",
			in:        "top 5 deadliest animals",
			wantTopic: "top 5 deadliest animals",
			wantMax:   0,
		},
		{
			name:      "max= prefix at start",
			in:        "max=50 build a video about quantum entanglement",
			wantTopic: "build a video about quantum entanglement",
			wantMax:   50,
		},
		{
			name:      "double-dash flag at start",
			in:        "--max=25 short on neural networks",
			wantTopic: "short on neural networks",
			wantMax:   25,
		},
		{
			name:      "single-dash flag at start",
			in:        "-max=15 a tiny task",
			wantTopic: "a tiny task",
			wantMax:   15,
		},
		{
			name:      "max= flag in the middle",
			in:        "build a video max=80 about astrophysics",
			wantTopic: "build a video about astrophysics",
			wantMax:   80,
		},
		{
			name:      "max= flag at the end",
			in:        "do the thing max=12",
			wantTopic: "do the thing",
			wantMax:   12,
		},
		{
			name:      "alias max_iter=",
			in:        "max_iter=33 task body",
			wantTopic: "task body",
			wantMax:   33,
		},
		{
			name:      "alias max-iterations=",
			in:        "--max-iterations=99 deep dive into rust",
			wantTopic: "deep dive into rust",
			wantMax:   99,
		},
		{
			name:      "case insensitive key",
			in:        "MAX=7 hello world",
			wantTopic: "hello world",
			wantMax:   7,
		},
		{
			name:      "non-numeric value is ignored",
			in:        "max=abc do something",
			wantTopic: "max=abc do something",
			wantMax:   0,
		},
		{
			name:      "max= at boundary doesn't eat unrelated text",
			in:        "the max altitude was 1000",
			wantTopic: "the max altitude was 1000",
			wantMax:   0,
		},
		{
			name:      "huge value is capped at 10000",
			in:        "max=99999999 absurd budget",
			wantTopic: "absurd budget",
			wantMax:   10000,
		},
		{
			name:      "extra whitespace is collapsed",
			in:        "  max=20    spaced     topic   ",
			wantTopic: "spaced topic",
			wantMax:   20,
		},
		{
			name:      "only flag, no topic",
			in:        "max=10",
			wantTopic: "",
			wantMax:   10,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotTopic, gotOpts := parseNewCommandArgs(c.in)
			if gotTopic != c.wantTopic {
				t.Errorf("topic = %q, want %q", gotTopic, c.wantTopic)
			}
			if gotOpts.MaxIterations != c.wantMax {
				t.Errorf("MaxIterations = %d, want %d", gotOpts.MaxIterations, c.wantMax)
			}
		})
	}
}

func TestSplitTelegramChunks(t *testing.T) {
	t.Run("short text is a single chunk", func(t *testing.T) {
		got := splitTelegramChunks("hello world", telegramMaxMessageBytes)
		if len(got) != 1 || got[0] != "hello world" {
			t.Fatalf("got %#v, want single chunk", got)
		}
	})

	t.Run("never splits a multi-byte rune (emoji + Turkish)", func(t *testing.T) {
		// Build an >4000-byte string with NO newlines, packed with multi-byte
		// runes so a naive byte cut would land mid-rune. Emoji are 4 bytes,
		// Turkish chars 2 bytes.
		unit := "çşğıöü🌊⚡🏳️‍🌈😳 "
		var b strings.Builder
		for b.Len() <= 12000 {
			b.WriteString(unit)
		}
		full := b.String()

		chunks := splitTelegramChunks(full, telegramMaxMessageBytes)
		if len(chunks) < 2 {
			t.Fatalf("expected multiple chunks, got %d", len(chunks))
		}
		for i, c := range chunks {
			if len(c) > telegramMaxMessageBytes {
				t.Errorf("chunk %d is %d bytes, exceeds limit %d", i, len(c), telegramMaxMessageBytes)
			}
			if !utf8.ValidString(c) {
				t.Errorf("chunk %d is not valid UTF-8 (rune was split)", i)
			}
		}
		if joined := strings.Join(chunks, ""); joined != full {
			t.Errorf("rejoined chunks != original (lossy split)")
		}
	})

	t.Run("prefers newline boundaries", func(t *testing.T) {
		line := strings.Repeat("a", 3000) + "\n"
		full := line + strings.Repeat("b", 3000)
		chunks := splitTelegramChunks(full, telegramMaxMessageBytes)
		if len(chunks) != 2 {
			t.Fatalf("expected 2 chunks, got %d", len(chunks))
		}
		if !strings.HasSuffix(chunks[0], "\n") {
			t.Errorf("first chunk should end at the newline boundary")
		}
		if strings.Join(chunks, "") != full {
			t.Errorf("rejoined chunks != original")
		}
	})
}

func TestAtoiSafe(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"0", 0},
		{"7", 7},
		{"100", 100},
		{"10000", 10000},
		{"10001", 10000}, // capped
		{"99999999", 10000},
		{"abc", 0},
		{"12a", 0},
		{"-5", 0}, // sign chars rejected
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := atoiSafe(c.in); got != c.want {
				t.Errorf("atoiSafe(%q) = %d, want %d", c.in, got, c.want)
			}
		})
	}
}
