package server

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestSplitSourceToRepoAndPath(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		wantRepo string
		wantPath string
	}{
		// ── SSH SCP-style with .git suffix ──
		{
			name:     "ssh scp-style with .git",
			source:   "git@github.com:user/repo.git/path/to/file.md",
			wantRepo: "git@github.com:user/repo.git",
			wantPath: "path/to/file.md",
		},
		{
			name:     "ssh scp-style with .git deep path",
			source:   "git@github.com:org/my-repo.git/docs/architecture/overview.md",
			wantRepo: "git@github.com:org/my-repo.git",
			wantPath: "docs/architecture/overview.md",
		},
		{
			name:     "ssh scp-style with .git single file",
			source:   "git@github.com:user/repo.git/README.md",
			wantRepo: "git@github.com:user/repo.git",
			wantPath: "README.md",
		},
		{
			name:     "ssh scp-style custom host with .git",
			source:   "git@gitlab.company.com:team/project.git/src/main.go",
			wantRepo: "git@gitlab.company.com:team/project.git",
			wantPath: "src/main.go",
		},
		// ── SSH SCP-style without .git suffix ──
		{
			name:     "ssh scp-style without .git",
			source:   "git@github.com:user/repo/path/to/file.md",
			wantRepo: "git@github.com:user/repo",
			wantPath: "path/to/file.md",
		},
		// ── SSH URI-style ──
		{
			name:     "ssh uri-style with .git",
			source:   "ssh://git@github.com/user/repo.git/path/to/file.md",
			wantRepo: "ssh://git@github.com/user/repo.git",
			wantPath: "path/to/file.md",
		},
		{
			name:     "ssh uri-style without .git",
			source:   "ssh://git@github.com/user/repo/path/to/file.md",
			wantRepo: "ssh://git@github.com/user/repo",
			wantPath: "path/to/file.md",
		},
		// ── HTTPS GitHub URLs ──
		{
			name:     "https github plain",
			source:   "https://github.com/user/repo/path/to/file.go",
			wantRepo: "https://github.com/user/repo",
			wantPath: "path/to/file.go",
		},
		{
			name:     "https github blob",
			source:   "https://github.com/user/repo/blob/main/path/to/file.go",
			wantRepo: "https://github.com/user/repo",
			wantPath: "path/to/file.go",
		},
		{
			name:     "http github plain",
			source:   "http://github.com/user/repo/docs/readme.md",
			wantRepo: "https://github.com/user/repo",
			wantPath: "docs/readme.md",
		},
		// ── Edge cases ──
		{
			name:     "empty string",
			source:   "",
			wantRepo: "",
			wantPath: "",
		},
		{
			name:     "plain filename",
			source:   "document.pdf",
			wantRepo: "",
			wantPath: "",
		},
		{
			name:     "https non-github",
			source:   "https://example.com/files/doc.md",
			wantRepo: "",
			wantPath: "",
		},
		{
			name:     "ssh repo without file path",
			source:   "git@github.com:user/repo.git",
			wantRepo: "",
			wantPath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRepo, gotPath := splitSourceToRepoAndPath(tt.source)
			if gotRepo != tt.wantRepo {
				t.Errorf("splitSourceToRepoAndPath(%q) repo = %q, want %q", tt.source, gotRepo, tt.wantRepo)
			}
			if gotPath != tt.wantPath {
				t.Errorf("splitSourceToRepoAndPath(%q) path = %q, want %q", tt.source, gotPath, tt.wantPath)
			}
		})
	}
}

func TestIsSSHSource(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{"git@github.com:user/repo.git/file.md", true},
		{"git@github.com:user/repo/file.md", true},
		{"git@gitlab.company.com:team/project.git/src/main.go", true},
		{"ssh://git@github.com/user/repo.git/file.md", true},
		{"ssh://git@github.com/user/repo/file.md", true},
		{"https://github.com/user/repo/file.md", false},
		{"http://github.com/user/repo/file.md", false},
		{"document.pdf", false},
		{"", false},
		{"/local/path/file.md", false},
	}

	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			got := isSSHSource(tt.source)
			if got != tt.want {
				t.Errorf("isSSHSource(%q) = %v, want %v", tt.source, got, tt.want)
			}
		})
	}
}

func TestHashCacheKey(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		branch  string
		want    string
	}{
		{
			name:    "ssh scp-style repo",
			repoURL: "git@github.com:user/repo.git",
			branch:  "main",
		},
		{
			name:    "https repo",
			repoURL: "https://github.com/user/repo.git",
			branch:  "main",
		},
		{
			name:    "different branch",
			repoURL: "git@github.com:org/project.git",
			branch:  "develop",
		},
		{
			name:    "empty branch",
			repoURL: "git@github.com:user/repo.git",
			branch:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hashCacheKey(tt.repoURL, tt.branch)

			// Verify output matches the expected format: SHA-256 of (repoURL + "\x00" + branch), first 8 bytes hex-encoded → 16 hex chars.
			h := sha256.Sum256([]byte(tt.repoURL + "\x00" + tt.branch))
			want := hex.EncodeToString(h[:8])

			if got != want {
				t.Errorf("hashCacheKey(%q, %q) = %q, want %q", tt.repoURL, tt.branch, got, want)
			}
			if len(got) != 16 {
				t.Errorf("hashCacheKey(%q, %q) length = %d, want 16", tt.repoURL, tt.branch, len(got))
			}
		})
	}

	// Verify different inputs produce different hashes.
	h1 := hashCacheKey("git@github.com:user/repo.git", "main")
	h2 := hashCacheKey("git@github.com:user/repo.git", "develop")
	h3 := hashCacheKey("git@github.com:other/repo.git", "main")
	if h1 == h2 {
		t.Errorf("same repo different branches should produce different hashes: %q == %q", h1, h2)
	}
	if h1 == h3 {
		t.Errorf("different repos same branch should produce different hashes: %q == %q", h1, h3)
	}
}

func TestSplitSSHSource(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		wantRepo string
		wantPath string
	}{
		{
			name:     "scp with .git",
			source:   "git@github.com:user/repo.git/path/to/file.md",
			wantRepo: "git@github.com:user/repo.git",
			wantPath: "path/to/file.md",
		},
		{
			name:     "scp without .git",
			source:   "git@github.com:user/repo/path/to/file.md",
			wantRepo: "git@github.com:user/repo",
			wantPath: "path/to/file.md",
		},
		{
			name:     "uri with .git",
			source:   "ssh://git@github.com/user/repo.git/docs/file.md",
			wantRepo: "ssh://git@github.com/user/repo.git",
			wantPath: "docs/file.md",
		},
		{
			name:     "uri without .git",
			source:   "ssh://git@github.com/user/repo/docs/file.md",
			wantRepo: "ssh://git@github.com/user/repo",
			wantPath: "docs/file.md",
		},
		{
			name:     "no file path scp",
			source:   "git@github.com:user/repo.git",
			wantRepo: "",
			wantPath: "",
		},
		{
			name:     "not ssh",
			source:   "https://github.com/user/repo/file.md",
			wantRepo: "",
			wantPath: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotRepo, gotPath := splitSSHSource(tt.source)
			if gotRepo != tt.wantRepo {
				t.Errorf("splitSSHSource(%q) repo = %q, want %q", tt.source, gotRepo, tt.wantRepo)
			}
			if gotPath != tt.wantPath {
				t.Errorf("splitSSHSource(%q) path = %q, want %q", tt.source, gotPath, tt.wantPath)
			}
		})
	}
}
