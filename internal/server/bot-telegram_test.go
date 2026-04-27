package server

import "testing"

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
