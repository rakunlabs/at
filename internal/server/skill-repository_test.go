package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFetchGitSkillPackagesImportsMultipleSkillsAndResources(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, filepath.Join(repo, "skills", "alpha", "SKILL.md"), "---\nname: alpha\ndescription: Alpha skill\n---\n\nRead references/guide.md when needed.\n")
	writeTestFile(t, filepath.Join(repo, "skills", "alpha", "references", "guide.md"), "Alpha guide\n")
	writeTestFile(t, filepath.Join(repo, "skills", "beta", "SKILL.md"), "---\nname: beta\ndescription: Beta skill\n---\n\nBeta instructions.\n")
	writeTestFile(t, filepath.Join(repo, "skills", "beta", "references", "notes.md"), "Beta notes\n")
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "skills")

	packages, err := (&Server{}).fetchGitSkillPackages(context.Background(), skillImportSource{
		URL: repo, Repository: true, Path: "skills", ImportAll: true,
	})
	if err != nil {
		t.Fatalf("fetchGitSkillPackages: %v", err)
	}
	if len(packages) != 2 {
		t.Fatalf("packages = %d, want 2", len(packages))
	}
	if packages[0].Export.Name != "alpha" || packages[0].Path != "skills/alpha" {
		t.Fatalf("first package = %+v", packages[0])
	}
	if len(packages[0].Export.Resources) != 1 || packages[0].Export.Resources[0].Path != "references/guide.md" {
		t.Fatalf("alpha resources = %+v", packages[0].Export.Resources)
	}
	if packages[0].Checksum == "" || packages[0].Checksum == packages[1].Checksum {
		t.Fatalf("package checksums = %q / %q", packages[0].Checksum, packages[1].Checksum)
	}
}

func TestFetchGitSkillPackagesRequiresSelectionForMultipleSkills(t *testing.T) {
	repo := t.TempDir()
	writeTestFile(t, filepath.Join(repo, "a", "SKILL.md"), "---\nname: a\n---\nA\n")
	writeTestFile(t, filepath.Join(repo, "b", "SKILL.md"), "---\nname: b\n---\nB\n")
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "skills")

	_, err := (&Server{}).fetchGitSkillPackages(context.Background(), skillImportSource{URL: repo, Repository: true})
	if err == nil {
		t.Fatal("expected multiple-skills selection error")
	}
}

func TestRepositorySkillRootRejectsTraversal(t *testing.T) {
	if _, err := repositorySkillRoot(t.TempDir(), "../outside"); err == nil {
		t.Fatal("expected traversal error")
	}
}

func TestReadSkillPackageIncludesBinaryFilesInChecksum(t *testing.T) {
	repo := t.TempDir()
	mainFile := filepath.Join(repo, "SKILL.md")
	binaryFile := filepath.Join(repo, "asset.bin")
	writeTestFile(t, mainFile, "---\nname: binary-checksum\n---\nInstructions\n")
	if err := os.WriteFile(binaryFile, []byte{0, 1, 2}, 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := readSkillPackage(repo, mainFile, map[string]bool{repo: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Export.Resources) != 0 {
		t.Fatalf("binary resource was exposed: %+v", first.Export.Resources)
	}
	if err := os.WriteFile(binaryFile, []byte{0, 1, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := readSkillPackage(repo, mainFile, map[string]bool{repo: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.Checksum == second.Checksum {
		t.Fatal("binary resource change did not change package checksum")
	}
}

func writeTestFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	// Test repositories must not inherit signing or hooks from the developer's
	// global Git configuration. In particular, commit.gpgSign can otherwise
	// open an interactive PIN prompt during go test.
	gitArgs := []string{"-c", "commit.gpgSign=false", "-c", "tag.gpgSign=false", "-c", "core.hooksPath=/dev/null"}
	cmd := exec.Command("git", append(gitArgs, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}
