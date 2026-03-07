package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ─── File Tool Executors ───
//
// These executors implement OpenCode-style file manipulation tools.
// They run server-side and are called via the builtin tool dispatch.

// execFileRead reads a file with optional line range.
// Parameters: file_path (string, required), offset (int, optional), limit (int, optional)
func (s *Server) execFileRead(_ context.Context, args map[string]any) (string, error) {
	filePath, _ := args["file_path"].(string)
	if filePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", filePath)
		}
		return "", fmt.Errorf("cannot access path: %w", err)
	}

	// If it's a directory, list contents.
	if info.IsDir() {
		return s.readDirectory(filePath)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	totalLines := len(lines)

	// Parse offset (1-indexed).
	offset := 1
	if o, ok := args["offset"].(float64); ok && int(o) > 0 {
		offset = int(o)
	}

	// Parse limit.
	limit := 2000
	if l, ok := args["limit"].(float64); ok && int(l) > 0 {
		limit = int(l)
	}

	// Clamp offset.
	if offset > totalLines {
		return fmt.Sprintf("File has %d lines, offset %d is beyond end of file.", totalLines, offset), nil
	}

	// Extract the requested range (offset is 1-indexed).
	startIdx := offset - 1
	endIdx := startIdx + limit
	if endIdx > totalLines {
		endIdx = totalLines
	}

	var sb strings.Builder
	for i := startIdx; i < endIdx; i++ {
		lineNum := i + 1
		sb.WriteString(fmt.Sprintf("%d: %s\n", lineNum, lines[i]))
	}

	if endIdx < totalLines {
		sb.WriteString(fmt.Sprintf("\n(Showing lines %d-%d of %d. Use offset=%d to continue.)", offset, endIdx, totalLines, endIdx+1))
	}

	return sb.String(), nil
}

// readDirectory lists directory contents.
func (s *Server) readDirectory(dirPath string) (string, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return "", fmt.Errorf("failed to read directory: %w", err)
	}

	var sb strings.Builder
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			name += "/"
		}
		sb.WriteString(name + "\n")
	}

	return sb.String(), nil
}

// execFileWrite creates or overwrites a file.
// Parameters: file_path (string, required), content (string, required)
func (s *Server) execFileWrite(_ context.Context, args map[string]any) (string, error) {
	filePath, _ := args["file_path"].(string)
	if filePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	content, _ := args["content"].(string)

	// Create parent directories if they don't exist.
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	lineCount := strings.Count(content, "\n") + 1
	return fmt.Sprintf("Successfully wrote %d bytes (%d lines) to %s", len(content), lineCount, filePath), nil
}

// execFileEdit performs exact string replacement in a file.
// Parameters: file_path (string, required), old_string (string, required),
//
//	new_string (string, required), replace_all (bool, optional)
func (s *Server) execFileEdit(_ context.Context, args map[string]any) (string, error) {
	filePath, _ := args["file_path"].(string)
	if filePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	oldString, _ := args["old_string"].(string)
	newString, _ := args["new_string"].(string)

	if oldString == "" {
		return "", fmt.Errorf("old_string is required")
	}
	if oldString == newString {
		return "", fmt.Errorf("old_string and new_string must be different")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", filePath)
		}
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	content := string(data)

	// Check if old_string exists.
	count := strings.Count(content, oldString)
	if count == 0 {
		return "", fmt.Errorf("old_string not found in file %s", filePath)
	}

	replaceAll, _ := args["replace_all"].(bool)

	if count > 1 && !replaceAll {
		return "", fmt.Errorf("found %d matches for old_string in %s. Use replace_all=true to replace all, or provide more context to make the match unique", count, filePath)
	}

	var newContent string
	if replaceAll {
		newContent = strings.ReplaceAll(content, oldString, newString)
	} else {
		newContent = strings.Replace(content, oldString, newString, 1)
	}

	if err := os.WriteFile(filePath, []byte(newContent), 0o644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	replacements := count
	if !replaceAll {
		replacements = 1
	}

	// Show snippet around the edit.
	lines := strings.Split(newContent, "\n")
	totalLines := len(lines)

	return fmt.Sprintf("Successfully edited %s (%d replacement(s) made, %d total lines)", filePath, replacements, totalLines), nil
}

// execFileMultiEdit performs multiple sequential string replacements on a single file.
// Parameters: file_path (string, required), edits (array, required)
func (s *Server) execFileMultiEdit(_ context.Context, args map[string]any) (string, error) {
	filePath, _ := args["file_path"].(string)
	if filePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	editsRaw, ok := args["edits"]
	if !ok {
		return "", fmt.Errorf("edits is required")
	}

	editsJSON, err := json.Marshal(editsRaw)
	if err != nil {
		return "", fmt.Errorf("invalid edits format: %w", err)
	}

	var edits []struct {
		OldString  string `json:"old_string"`
		NewString  string `json:"new_string"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(editsJSON, &edits); err != nil {
		return "", fmt.Errorf("invalid edits format: %w", err)
	}

	if len(edits) == 0 {
		return "", fmt.Errorf("at least one edit is required")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", filePath)
		}
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	content := string(data)
	totalReplacements := 0

	for i, edit := range edits {
		if edit.OldString == "" {
			return "", fmt.Errorf("edit #%d: old_string is required", i+1)
		}
		if edit.OldString == edit.NewString {
			return "", fmt.Errorf("edit #%d: old_string and new_string must be different", i+1)
		}

		count := strings.Count(content, edit.OldString)
		if count == 0 {
			return "", fmt.Errorf("edit #%d: old_string not found in file", i+1)
		}

		if count > 1 && !edit.ReplaceAll {
			return "", fmt.Errorf("edit #%d: found %d matches. Use replace_all=true or provide more context", i+1, count)
		}

		if edit.ReplaceAll {
			content = strings.ReplaceAll(content, edit.OldString, edit.NewString)
			totalReplacements += count
		} else {
			content = strings.Replace(content, edit.OldString, edit.NewString, 1)
			totalReplacements++
		}
	}

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return fmt.Sprintf("Successfully applied %d edit(s) with %d replacement(s) to %s", len(edits), totalReplacements, filePath), nil
}

// execFilePatch applies a unified diff/patch to a file.
// Parameters: file_path (string, required), diff (string, required)
func (s *Server) execFilePatch(ctx context.Context, args map[string]any) (string, error) {
	filePath, _ := args["file_path"].(string)
	if filePath == "" {
		return "", fmt.Errorf("file_path is required")
	}

	diff, _ := args["diff"].(string)
	if diff == "" {
		return "", fmt.Errorf("diff is required")
	}

	// Verify the target file exists.
	if _, err := os.Stat(filePath); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file not found: %s", filePath)
		}
		return "", fmt.Errorf("cannot access file: %w", err)
	}

	// Apply via the patch command.
	timeout := 30 * time.Second

	cmd := exec.CommandContext(ctx, "patch", "--no-backup-if-mismatch", "-u", filePath)
	cmd.Stdin = strings.NewReader(diff)

	timer := time.AfterFunc(timeout, func() { _ = cmd.Process.Kill() })
	defer timer.Stop()

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("patch failed: %s\n%s", err, string(output))
	}

	return fmt.Sprintf("Patch applied successfully to %s\n%s", filePath, strings.TrimSpace(string(output))), nil
}

// execFileGlob finds files by glob pattern.
// Parameters: pattern (string, required), path (string, optional)
func (s *Server) execFileGlob(_ context.Context, args map[string]any) (string, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	searchPath, _ := args["path"].(string)
	if searchPath == "" {
		searchPath = "."
	}

	// Resolve to absolute path.
	searchPath, err := filepath.Abs(searchPath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	const maxResults = 200

	type fileEntry struct {
		path  string
		mtime time.Time
	}

	var files []fileEntry
	truncated := false

	err = filepath.WalkDir(searchPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}

		// Skip hidden directories (except the root).
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && path != searchPath {
			return filepath.SkipDir
		}

		// Skip common vendor/build directories.
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "vendor", ".git", "__pycache__", "dist", "build":
				return filepath.SkipDir
			}
		}

		relPath, err := filepath.Rel(searchPath, path)
		if err != nil {
			return nil
		}

		// Match against the pattern.
		matched, err := filepath.Match(pattern, d.Name())
		if err != nil {
			return nil
		}

		// Also try matching the full relative path for patterns like "**/*.go".
		if !matched {
			matched, _ = filepath.Match(pattern, relPath)
		}

		if matched {
			if len(files) >= maxResults {
				truncated = true
				return filepath.SkipAll
			}
			info, _ := d.Info()
			mtime := time.Time{}
			if info != nil {
				mtime = info.ModTime()
			}
			files = append(files, fileEntry{path: path, mtime: mtime})
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("glob search failed: %w", err)
	}

	// Sort by modification time (newest first).
	sort.Slice(files, func(i, j int) bool {
		return files[i].mtime.After(files[j].mtime)
	})

	if len(files) == 0 {
		return "No files found matching pattern: " + pattern, nil
	}

	var sb strings.Builder
	for _, f := range files {
		sb.WriteString(f.path + "\n")
	}

	if truncated {
		sb.WriteString(fmt.Sprintf("\n(Results truncated: showing first %d results)", maxResults))
	}

	return sb.String(), nil
}

// execFileGrep searches file contents using regular expressions.
// Parameters: pattern (string, required), path (string, optional), include (string, optional)
func (s *Server) execFileGrep(_ context.Context, args map[string]any) (string, error) {
	pattern, _ := args["pattern"].(string)
	if pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex pattern: %w", err)
	}

	searchPath, _ := args["path"].(string)
	if searchPath == "" {
		searchPath = "."
	}

	searchPath, err = filepath.Abs(searchPath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	includePattern, _ := args["include"].(string)

	const maxMatches = 200

	type grepMatch struct {
		file  string
		line  int
		text  string
		mtime time.Time
	}

	var matches []grepMatch
	truncated := false

	err = filepath.WalkDir(searchPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		// Skip hidden directories.
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") && path != searchPath {
			return filepath.SkipDir
		}

		// Skip common large directories.
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "vendor", ".git", "__pycache__", "dist", "build":
				return filepath.SkipDir
			}
		}

		if d.IsDir() {
			return nil
		}

		// Apply include filter.
		if includePattern != "" {
			matched, _ := filepath.Match(includePattern, d.Name())
			if !matched {
				return nil
			}
		}

		// Skip binary files by checking extension.
		ext := strings.ToLower(filepath.Ext(d.Name()))
		binaryExts := map[string]bool{
			".exe": true, ".bin": true, ".so": true, ".dll": true,
			".dylib": true, ".png": true, ".jpg": true, ".jpeg": true,
			".gif": true, ".ico": true, ".pdf": true, ".zip": true,
			".tar": true, ".gz": true, ".woff": true, ".woff2": true,
			".ttf": true, ".eot": true, ".mp3": true, ".mp4": true,
			".wav": true, ".db": true, ".sqlite": true,
		}
		if binaryExts[ext] {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		info, _ := d.Info()
		mtime := time.Time{}
		if info != nil {
			mtime = info.ModTime()
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if re.MatchString(line) {
				if len(matches) >= maxMatches {
					truncated = true
					return filepath.SkipAll
				}
				// Truncate long lines.
				displayLine := line
				if len(displayLine) > 200 {
					displayLine = displayLine[:200] + "..."
				}
				matches = append(matches, grepMatch{
					file:  path,
					line:  i + 1,
					text:  displayLine,
					mtime: mtime,
				})
			}
		}

		return nil
	})
	if err != nil {
		return "", fmt.Errorf("grep search failed: %w", err)
	}

	// Sort by file modification time (newest first).
	sort.Slice(matches, func(i, j int) bool {
		return matches[i].mtime.After(matches[j].mtime)
	})

	if len(matches) == 0 {
		return "No matches found for pattern: " + pattern, nil
	}

	// Group by file.
	var sb strings.Builder
	currentFile := ""
	for _, m := range matches {
		if m.file != currentFile {
			if currentFile != "" {
				sb.WriteString("\n")
			}
			sb.WriteString(m.file + ":\n")
			currentFile = m.file
		}
		sb.WriteString(fmt.Sprintf("  Line %d: %s\n", m.line, m.text))
	}

	if truncated {
		sb.WriteString(fmt.Sprintf("\n(Results truncated: showing first %d matches)", maxMatches))
	}

	return sb.String(), nil
}

// execFileList lists files and directories in a given path.
// Parameters: path (string, optional), pattern (string, optional)
func (s *Server) execFileList(_ context.Context, args map[string]any) (string, error) {
	dirPath, _ := args["path"].(string)
	if dirPath == "" {
		dirPath = "."
	}

	dirPath, err := filepath.Abs(dirPath)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	info, err := os.Stat(dirPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("path not found: %s", dirPath)
		}
		return "", fmt.Errorf("cannot access path: %w", err)
	}

	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", dirPath)
	}

	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return "", fmt.Errorf("failed to read directory: %w", err)
	}

	filterPattern, _ := args["pattern"].(string)

	var sb strings.Builder
	count := 0
	for _, entry := range entries {
		name := entry.Name()

		// Apply filter pattern if provided.
		if filterPattern != "" {
			matched, _ := filepath.Match(filterPattern, name)
			if !matched {
				continue
			}
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		typeStr := "file"
		if entry.IsDir() {
			typeStr = "dir"
			name += "/"
		} else if info.Mode()&os.ModeSymlink != 0 {
			typeStr = "link"
		}

		sb.WriteString(fmt.Sprintf("%-6s %8d  %s  %s\n", typeStr, info.Size(), info.ModTime().Format("2006-01-02 15:04"), name))
		count++
	}

	if count == 0 {
		if filterPattern != "" {
			return fmt.Sprintf("No entries matching '%s' in %s", filterPattern, dirPath), nil
		}
		return fmt.Sprintf("Empty directory: %s", dirPath), nil
	}

	return fmt.Sprintf("%s (%d entries)\n\n%s", dirPath, count, sb.String()), nil
}
