package service

import "testing"

func TestValidDeveloperBranch(t *testing.T) {
	tests := []struct {
		branch string
		valid  bool
	}{
		{branch: "feature/storage", valid: true},
		{branch: "release-1.2", valid: true},
		{branch: "-force"},
		{branch: "main:evil"},
		{branch: "refs//heads"},
		{branch: "topic.lock"},
		{branch: "topic@{1}"},
		{branch: "topic..other"},
		{branch: "@"},
	}
	for _, tt := range tests {
		t.Run(tt.branch, func(t *testing.T) {
			if got := ValidDeveloperBranch(tt.branch); got != tt.valid {
				t.Fatalf("ValidDeveloperBranch(%q) = %v, want %v", tt.branch, got, tt.valid)
			}
		})
	}
}
