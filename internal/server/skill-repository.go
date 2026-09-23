package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/rakunlabs/at/internal/service"
)

const (
	maxSkillPackageFiles = 256
	maxSkillResourceSize = 256 * 1024
	maxSkillPackageSize  = 2 * 1024 * 1024
)

type skillImportSource struct {
	URL          string `json:"url"`
	Repository   bool   `json:"repository"`
	Ref          string `json:"ref,omitempty"`
	Path         string `json:"path,omitempty"`
	ImportAll    bool   `json:"import_all,omitempty"`
	CredentialID string `json:"credential_id,omitempty"`
}

type fetchedSkillPackage struct {
	Export   *skillExportData
	Checksum string
	Path     string
}

func (s *Server) fetchSkillPackages(ctx context.Context, source skillImportSource) ([]fetchedSkillPackage, error) {
	if !source.Repository {
		export, checksum, err := s.fetchAndParseSkillURL(ctx, source.URL)
		if err != nil {
			return nil, err
		}
		return []fetchedSkillPackage{{Export: export, Checksum: checksum}}, nil
	}

	return s.fetchGitSkillPackages(ctx, source)
}

func (s *Server) fetchGitSkillPackages(ctx context.Context, source skillImportSource) ([]fetchedSkillPackage, error) {
	if strings.TrimSpace(source.URL) == "" {
		return nil, fmt.Errorf("repository URL is required")
	}

	tempDir, err := os.MkdirTemp("", "at-skill-repo-")
	if err != nil {
		return nil, fmt.Errorf("create temporary repository directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	repoDir := filepath.Join(tempDir, "repo")
	args := []string{"clone", "--depth", "1", "--single-branch"}
	if source.Ref != "" {
		args = append(args, "--branch", source.Ref)
	}
	args = append(args, "--", source.URL, repoDir)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cleanupCredential := func() {}
	if source.CredentialID != "" {
		credentialEnv, cleanup, err := s.gitCredentialEnvironment(ctx, source.URL, source.CredentialID)
		if err != nil {
			return nil, err
		}
		cleanupCredential = cleanup
		cmd.Env = append(cmd.Env, credentialEnv...)
	}
	defer cleanupCredential()
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("clone repository: %s: %w", strings.TrimSpace(string(out)), err)
	}

	root, err := repositorySkillRoot(repoDir, source.Path)
	if err != nil {
		return nil, err
	}
	mainFiles, err := findSkillMainFiles(root)
	if err != nil {
		return nil, err
	}
	if len(mainFiles) == 0 {
		return nil, fmt.Errorf("no SKILL.md found under %q", source.Path)
	}
	if !source.ImportAll {
		for _, mainFile := range mainFiles {
			if filepath.Dir(mainFile) == root {
				mainFiles = []string{mainFile}
				break
			}
		}
	}
	if !source.ImportAll && len(mainFiles) > 1 {
		paths := make([]string, 0, len(mainFiles))
		for _, file := range mainFiles {
			rel, _ := filepath.Rel(repoDir, file)
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil, fmt.Errorf("multiple skills found; set import_all or select a path: %s", strings.Join(paths, ", "))
	}
	if !source.ImportAll {
		mainFiles = mainFiles[:1]
	}

	skillDirs := make(map[string]bool, len(mainFiles))
	for _, mainFile := range mainFiles {
		skillDirs[filepath.Dir(mainFile)] = true
	}

	packages := make([]fetchedSkillPackage, 0, len(mainFiles))
	for _, mainFile := range mainFiles {
		pkg, err := readSkillPackage(repoDir, mainFile, skillDirs)
		if err != nil {
			return nil, err
		}
		packages = append(packages, pkg)
	}
	return packages, nil
}

func repositorySkillRoot(repoDir, requested string) (string, error) {
	requested = strings.TrimSpace(strings.ReplaceAll(requested, "\\", "/"))
	if requested == "" || requested == "." {
		return repoDir, nil
	}
	clean := pathpkg.Clean(requested)
	if clean == ".." || strings.HasPrefix(clean, "../") || pathpkg.IsAbs(clean) {
		return "", fmt.Errorf("skill path must stay inside the repository")
	}
	root := filepath.Join(repoDir, filepath.FromSlash(clean))
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve skill path %q: %w", requested, err)
	}
	if resolved != root {
		return "", fmt.Errorf("skill path may not contain symlinks")
	}
	info, err := os.Lstat(root)
	if err != nil {
		return "", fmt.Errorf("open skill path %q: %w", requested, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("skill path may not be a symlink")
	}
	if !info.IsDir() {
		if !isSkillMainName(info.Name()) {
			return "", fmt.Errorf("skill path must name a directory or SKILL.md")
		}
		return filepath.Dir(root), nil
	}
	return root, nil
}

func findSkillMainFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if !entry.IsDir() && isSkillMainName(entry.Name()) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan repository skills: %w", err)
	}
	sort.Strings(files)
	return files, nil
}

func isSkillMainName(name string) bool {
	return strings.EqualFold(name, "SKILL.md") || strings.EqualFold(name, "SKILLS.md")
}

func readSkillPackage(repoDir, mainFile string, skillDirs map[string]bool) (fetchedSkillPackage, error) {
	mainData, err := os.ReadFile(mainFile)
	if err != nil {
		return fetchedSkillPackage{}, fmt.Errorf("read %s: %w", mainFile, err)
	}
	if len(mainData) > maxSkillPackageSize {
		return fetchedSkillPackage{}, fmt.Errorf("SKILL.md exceeds %d bytes", maxSkillPackageSize)
	}
	export, err := skillExportFromSkillMD(mainData)
	if err != nil {
		return fetchedSkillPackage{}, fmt.Errorf("parse %s: %w", mainFile, err)
	}

	packageDir := filepath.Dir(mainFile)
	resources := make([]service.SkillResource, 0)
	type packageFile struct {
		path string
		data []byte
	}
	packageFiles := make([]packageFile, 0)
	total := len(mainData)
	filesSeen := 0
	err = filepath.WalkDir(packageDir, func(file string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if file != packageDir && entry.IsDir() && skillDirs[file] {
			return filepath.SkipDir
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || file == mainFile {
			return nil
		}
		filesSeen++
		if filesSeen > maxSkillPackageFiles {
			return fmt.Errorf("skill package exceeds %d resource files", maxSkillPackageFiles)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxSkillResourceSize {
			return fmt.Errorf("resource %q exceeds %d bytes", entry.Name(), maxSkillResourceSize)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		total += len(data)
		if total > maxSkillPackageSize {
			return fmt.Errorf("skill package exceeds %d bytes", maxSkillPackageSize)
		}
		rel, err := filepath.Rel(packageDir, file)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		packageFiles = append(packageFiles, packageFile{path: rel, data: data})
		if !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
			return nil // Binary assets affect package integrity but are not exposed as prompt text.
		}
		mediaType := mime.TypeByExtension(strings.ToLower(filepath.Ext(file)))
		resources = append(resources, service.SkillResource{Path: rel, Content: string(data), MediaType: mediaType})
		return nil
	})
	if err != nil {
		return fetchedSkillPackage{}, fmt.Errorf("read skill package: %w", err)
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].Path < resources[j].Path })
	export.Resources = resources

	hash := sha256.New()
	hash.Write([]byte(filepath.Base(mainFile)))
	hash.Write([]byte{0})
	hash.Write(mainData)
	sort.Slice(packageFiles, func(i, j int) bool { return packageFiles[i].path < packageFiles[j].path })
	for _, file := range packageFiles {
		hash.Write([]byte{0})
		hash.Write([]byte(file.path))
		hash.Write([]byte{0})
		hash.Write(file.data)
	}
	relDir, err := filepath.Rel(repoDir, packageDir)
	if err != nil {
		return fetchedSkillPackage{}, err
	}
	sourcePath := filepath.ToSlash(relDir)
	if sourcePath == "." {
		sourcePath = ""
	}
	return fetchedSkillPackage{Export: export, Checksum: hex.EncodeToString(hash.Sum(nil)), Path: sourcePath}, nil
}
