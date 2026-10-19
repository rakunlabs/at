package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rakunlabs/at/internal/service"
)

// Remote marketplaces are Claude Code plugin marketplaces (marketplace.json)
// read from a URL or a git repository. A plugin contributes skills
// (skills/*/SKILL.md) and MCP servers (.mcp.json or plugin.json mcpServers);
// both are installed selectively into AT and stay linked to their origin.

const (
	remoteMarketplaceMaxJSON    = 2 * 1024 * 1024
	remoteMarketplaceMaxArchive = 32 * 1024 * 1024
	remoteMarketplaceMaxFiles   = 4096
	remoteMarketplaceMaxPlugins = 500
)

var githubShorthand = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9._-]+$`)

// remotePluginSource is a plugin entry's `source`: a relative path string or a
// typed object. Unsupported types are listed but cannot be installed.
type remotePluginSource struct {
	Type   string `json:"source"`
	Path   string `json:"path,omitempty"`
	Repo   string `json:"repo,omitempty"`
	URL    string `json:"url,omitempty"`
	Ref    string `json:"ref,omitempty"`
	SHA    string `json:"sha,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

func (s *remotePluginSource) UnmarshalJSON(data []byte) error {
	var str string
	if err := json.Unmarshal(data, &str); err == nil {
		*s = remotePluginSource{Type: "relative", Path: str}
		return nil
	}
	type plain remotePluginSource
	var obj plain
	if err := json.Unmarshal(data, &obj); err != nil {
		return err
	}
	*s = remotePluginSource(obj)
	return nil
}

type remoteMarketplaceFile struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Metadata    struct {
		Description string `json:"description"`
		PluginRoot  string `json:"pluginRoot"`
	} `json:"metadata"`
	Plugins []remoteMarketplaceEntry `json:"plugins"`
}

type remoteMarketplaceEntry struct {
	Name        string             `json:"name"`
	DisplayName string             `json:"displayName"`
	Description string             `json:"description"`
	Version     string             `json:"version"`
	Category    string             `json:"category"`
	Tags        []string           `json:"tags"`
	Keywords    []string           `json:"keywords"`
	Source      remotePluginSource `json:"source"`
	MCPServers  json.RawMessage    `json:"mcpServers"`
}

// RemotePlugin is one plugin listed by a remote marketplace.
type RemotePlugin struct {
	Name        string   `json:"name"`
	DisplayName string   `json:"display_name,omitempty"`
	Description string   `json:"description,omitempty"`
	Version     string   `json:"version,omitempty"`
	Category    string   `json:"category,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	SourceType  string   `json:"source_type"`
	Installable bool     `json:"installable"`
	Reason      string   `json:"reason,omitempty"`
}

// RemoteMarketplace is the catalog of a source.
type RemoteMarketplace struct {
	Source      service.MarketplaceSource `json:"source"`
	Name        string                    `json:"name"`
	Description string                    `json:"description,omitempty"`
	Plugins     []RemotePlugin            `json:"plugins"`
}

// RemotePluginSkill is a skill package found inside a plugin.
type RemotePluginSkill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
	Checksum    string `json:"checksum"`
	Resources   int    `json:"resources"`
	// Installed names the local skill already linked to this package.
	InstalledID     string `json:"installed_id,omitempty"`
	UpdateAvailable bool   `json:"update_available,omitempty"`
}

// RemotePluginMCP is an MCP server declared by a plugin.
type RemotePluginMCP struct {
	Name     string              `json:"name"`
	Type     string              `json:"type"` // http | stdio
	Upstream service.MCPUpstream `json:"upstream"`
	// Variables lists ${VAR} placeholders the installer must map to AT
	// variables ({{var:key}}) or literal values.
	Variables []string `json:"variables,omitempty"`
	// PluginRelative is true when a stdio command or argument refers to the
	// plugin directory; AT cannot run such a server without the plugin files.
	PluginRelative bool `json:"plugin_relative,omitempty"`
}

// RemotePluginDetail is the inspected content of one plugin.
type RemotePluginDetail struct {
	Plugin     RemotePlugin        `json:"plugin"`
	Skills     []RemotePluginSkill `json:"skills"`
	MCPServers []RemotePluginMCP   `json:"mcp_servers"`
	Warnings   []string            `json:"warnings,omitempty"`
}

// remoteCatalog is a loaded marketplace; Root is set when the marketplace's
// own files are available (git sources) so relative plugin sources resolve.
type remoteCatalog struct {
	File    remoteMarketplaceFile
	Root    string
	cleanup func()
}

func (c *remoteCatalog) Close() {
	if c != nil && c.cleanup != nil {
		c.cleanup()
	}
}

// normalizeMarketplaceSource validates a source and expands GitHub shorthand.
func normalizeMarketplaceSource(src *service.MarketplaceSource) error {
	src.Name = strings.TrimSpace(src.Name)
	src.URL = strings.TrimSpace(src.URL)
	src.Ref = strings.TrimSpace(src.Ref)
	src.Path = strings.Trim(strings.TrimSpace(src.Path), "/")
	src.Type = strings.TrimSpace(src.Type)
	if src.Type == service.MarketplaceSourceGeneric {
		return nil
	}
	if src.URL == "" {
		return fmt.Errorf("url is required")
	}
	if repo, ref, _ := strings.Cut(src.URL, "@"); githubShorthand.MatchString(repo) {
		src.URL = "https://github.com/" + strings.TrimSuffix(repo, ".git") + ".git"
		if ref != "" && src.Ref == "" {
			src.Ref = ref
		}
		src.Type = service.MarketplaceSourceGit
	}
	if src.Type == "" {
		src.Type = service.MarketplaceSourceGit
		if strings.HasSuffix(strings.ToLower(strings.SplitN(src.URL, "?", 2)[0]), ".json") {
			src.Type = service.MarketplaceSourceURL
		}
	}
	switch src.Type {
	case service.MarketplaceSourceURL:
		u, err := url.Parse(src.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return fmt.Errorf("a marketplace.json URL must be https")
		}
		if src.CredentialID != "" {
			return fmt.Errorf("credentials for Git apply to repository sources only")
		}
	case service.MarketplaceSourceGit:
		if err := validateRemoteGitURL(src.URL); err != nil {
			return err
		}
		if src.Path != "" {
			clean := pathpkg.Clean(src.Path)
			if clean == ".." || strings.HasPrefix(clean, "../") || pathpkg.IsAbs(clean) {
				return fmt.Errorf("path must stay inside the repository")
			}
			src.Path = clean
		}
	default:
		return fmt.Errorf("unsupported source type %q (use url or git)", src.Type)
	}
	if src.Name == "" {
		src.Name = strings.TrimSuffix(pathpkg.Base(strings.TrimSuffix(src.URL, "/")), ".git")
	}
	return nil
}

// validateRemoteGitURL refuses local paths and transports that would let a
// marketplace URL read the server's own filesystem or run helpers.
func validateRemoteGitURL(raw string) error {
	if strings.HasPrefix(raw, "-") {
		return fmt.Errorf("invalid repository URL")
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" {
		switch u.Scheme {
		case "https", "ssh":
			if u.Host == "" {
				return fmt.Errorf("repository URL needs a host")
			}
			return nil
		default:
			return fmt.Errorf("repository URL must use https or ssh")
		}
	}
	// scp-like user@host:path
	if at := strings.Index(raw, "@"); at > 0 {
		if colon := strings.Index(raw[at:], ":"); colon > 1 && !strings.Contains(raw[:at], "/") {
			return nil
		}
	}
	return fmt.Errorf("repository URL must be https://, ssh:// or user@host:path (or GitHub owner/repo)")
}

// loadRemoteCatalog fetches a source's marketplace.json. For git sources
// without a marketplace file, a repository holding SKILL.md packages is
// presented as a single synthetic plugin so plain skill repositories work.
func (s *Server) loadRemoteCatalog(ctx context.Context, src service.MarketplaceSource) (*remoteCatalog, error) {
	switch src.Type {
	case service.MarketplaceSourceURL:
		data, err := fetchPublicBytes(ctx, src.URL, remoteMarketplaceMaxJSON)
		if err != nil {
			return nil, fmt.Errorf("fetch marketplace: %w", err)
		}
		file, err := parseRemoteMarketplace(data)
		if err != nil {
			return nil, err
		}
		return &remoteCatalog{File: file}, nil
	case service.MarketplaceSourceGit:
		repoDir, cleanup, err := s.cloneRepository(ctx, src.URL, src.Ref, src.CredentialID)
		if err != nil {
			return nil, err
		}
		cat := &remoteCatalog{Root: repoDir, cleanup: cleanup}
		manifest := src.Path
		if manifest == "" {
			manifest = ".claude-plugin/marketplace.json"
		} else if !strings.HasSuffix(manifest, ".json") {
			manifest = pathpkg.Join(manifest, ".claude-plugin/marketplace.json")
		}
		data, err := readContainedFile(repoDir, manifest, remoteMarketplaceMaxJSON)
		if errors.Is(err, os.ErrNotExist) {
			// A plain skills repository: one plugin rooted at the repository.
			base := strings.TrimSuffix(pathpkg.Base(strings.TrimSuffix(src.URL, "/")), ".git")
			cat.File = remoteMarketplaceFile{Name: src.Name, Plugins: []remoteMarketplaceEntry{{
				Name: slugifyClaudeName(base, "skills"), Description: "Skills in this repository",
				Source: remotePluginSource{Type: "relative", Path: "./" + strings.TrimSuffix(src.Path, "/")},
			}}}
			if src.Path == "" {
				cat.File.Plugins[0].Source.Path = "."
			}
			return cat, nil
		}
		if err != nil {
			cat.Close()
			return nil, fmt.Errorf("read %s: %w", manifest, err)
		}
		file, err := parseRemoteMarketplace(data)
		if err != nil {
			cat.Close()
			return nil, err
		}
		// Relative plugin sources resolve from the directory holding
		// .claude-plugin/, i.e. two levels above the manifest.
		rootRel := pathpkg.Dir(pathpkg.Dir(manifest))
		if rootRel != "." {
			cat.Root = filepath.Join(repoDir, filepath.FromSlash(rootRel))
		}
		cat.File = file
		return cat, nil
	default:
		return nil, fmt.Errorf("source type %q cannot be browsed; edit it to a marketplace URL or repository", src.Type)
	}
}

func parseRemoteMarketplace(data []byte) (remoteMarketplaceFile, error) {
	var file remoteMarketplaceFile
	if err := json.Unmarshal(data, &file); err != nil {
		return file, fmt.Errorf("not a marketplace.json: %w", err)
	}
	if file.Plugins == nil {
		return file, fmt.Errorf("not a marketplace.json: no plugins array")
	}
	if len(file.Plugins) > remoteMarketplaceMaxPlugins {
		file.Plugins = file.Plugins[:remoteMarketplaceMaxPlugins]
	}
	if file.Description == "" {
		file.Description = file.Metadata.Description
	}
	return file, nil
}

func (cat *remoteCatalog) summary(src service.MarketplaceSource) RemoteMarketplace {
	out := RemoteMarketplace{Source: src, Name: cat.File.Name, Description: cat.File.Description, Plugins: []RemotePlugin{}}
	for _, entry := range cat.File.Plugins {
		out.Plugins = append(out.Plugins, cat.pluginSummary(entry))
	}
	sort.SliceStable(out.Plugins, func(i, j int) bool { return out.Plugins[i].Name < out.Plugins[j].Name })
	return out
}

func (cat *remoteCatalog) pluginSummary(entry remoteMarketplaceEntry) RemotePlugin {
	p := RemotePlugin{
		Name: entry.Name, DisplayName: entry.DisplayName, Description: entry.Description,
		Version: entry.Version, Category: entry.Category, Tags: append(entry.Tags, entry.Keywords...),
		SourceType: entry.Source.Type, Installable: true,
	}
	switch entry.Source.Type {
	case "relative":
		if cat.Root == "" {
			p.Installable, p.Reason = false, "relative source needs a repository marketplace"
		}
	case "github", "url", "git-subdir", "archive":
	case "":
		p.Installable, p.Reason = false, "missing source"
	default:
		p.Installable, p.Reason = false, fmt.Sprintf("%s sources are not supported", entry.Source.Type)
	}
	return p
}

func (cat *remoteCatalog) entry(name string) (remoteMarketplaceEntry, bool) {
	for _, entry := range cat.File.Plugins {
		if entry.Name == name {
			return entry, true
		}
	}
	return remoteMarketplaceEntry{}, false
}

// materializePlugin makes the plugin's files available on disk and returns
// their directory. cleanup must be called by the caller.
func (s *Server) materializePlugin(ctx context.Context, cat *remoteCatalog, src service.MarketplaceSource, entry remoteMarketplaceEntry) (string, func(), error) {
	noop := func() {}
	ps := entry.Source
	switch ps.Type {
	case "relative":
		if cat.Root == "" {
			return "", nil, fmt.Errorf("plugin %q uses a relative source, which needs a repository marketplace", entry.Name)
		}
		rel := ps.Path
		if !strings.HasPrefix(rel, "./") && rel != "." && !strings.Contains(rel, "/") && cat.File.Metadata.PluginRoot != "" {
			rel = pathpkg.Join(cat.File.Metadata.PluginRoot, rel)
		}
		dir, err := containedDir(cat.Root, rel)
		if err != nil {
			return "", nil, fmt.Errorf("plugin %q: %w", entry.Name, err)
		}
		return dir, noop, nil
	case "github", "url", "git-subdir":
		repoURL := ps.URL
		if ps.Type == "github" || (ps.Type == "git-subdir" && githubShorthand.MatchString(repoURL)) {
			repo := ps.Repo
			if repo == "" {
				repo = repoURL
			}
			if !githubShorthand.MatchString(repo) {
				return "", nil, fmt.Errorf("plugin %q: invalid GitHub repository %q", entry.Name, repo)
			}
			repoURL = "https://github.com/" + strings.TrimSuffix(repo, ".git") + ".git"
		}
		if err := validateRemoteGitURL(repoURL); err != nil {
			return "", nil, fmt.Errorf("plugin %q: %w", entry.Name, err)
		}
		// The marketplace's deploy key is reused only for the same host.
		credential := ""
		if src.CredentialID != "" && sameGitHost(repoURL, src.URL) {
			credential = src.CredentialID
		}
		repoDir, cleanup, err := s.cloneRepository(ctx, repoURL, ps.Ref, credential)
		if err != nil {
			return "", nil, fmt.Errorf("plugin %q: %w", entry.Name, err)
		}
		if ps.Type != "git-subdir" {
			return repoDir, cleanup, nil
		}
		dir, err := containedDir(repoDir, ps.Path)
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("plugin %q: %w", entry.Name, err)
		}
		return dir, cleanup, nil
	case "archive":
		data, err := fetchPublicBytes(ctx, ps.URL, remoteMarketplaceMaxArchive)
		if err != nil {
			return "", nil, fmt.Errorf("plugin %q: download archive: %w", entry.Name, err)
		}
		if ps.SHA256 != "" {
			sum := sha256.Sum256(data)
			if !strings.EqualFold(hex.EncodeToString(sum[:]), ps.SHA256) {
				return "", nil, fmt.Errorf("plugin %q: archive sha256 mismatch", entry.Name)
			}
		}
		dir, cleanup, err := extractPluginZip(data)
		if err != nil {
			return "", nil, fmt.Errorf("plugin %q: %w", entry.Name, err)
		}
		return dir, cleanup, nil
	default:
		return "", nil, fmt.Errorf("plugin %q: %s sources are not supported", entry.Name, ps.Type)
	}
}

func sameGitHost(a, b string) bool {
	ha, _, errA := gitRepositoryEndpoint(a)
	hb, _, errB := gitRepositoryEndpoint(b)
	if errA == nil && errB == nil {
		return ha == hb
	}
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	return errA == nil && errB == nil && ua.Host != "" && strings.EqualFold(ua.Host, ub.Host)
}

// inspectPlugin reads skills and MCP servers from a materialized plugin.
func inspectPluginDir(dir string, entry remoteMarketplaceEntry) (RemotePluginDetail, []fetchedSkillPackage, error) {
	detail := RemotePluginDetail{Skills: []RemotePluginSkill{}, MCPServers: []RemotePluginMCP{}}

	var packages []fetchedSkillPackage
	if mainFiles, err := findSkillMainFiles(dir); err == nil && len(mainFiles) > 0 {
		pkgs, err := selectSkillPackages(dir, "", true)
		if err != nil {
			detail.Warnings = append(detail.Warnings, fmt.Sprintf("skills: %v", err))
		} else {
			packages = pkgs
		}
	}
	for _, pkg := range packages {
		detail.Skills = append(detail.Skills, RemotePluginSkill{
			Name: pkg.Export.Name, Description: pkg.Export.Description, Path: pkg.Path,
			Checksum: pkg.Checksum, Resources: len(pkg.Export.Resources),
		})
	}

	servers := map[string]json.RawMessage{}
	mergeMCP := func(raw json.RawMessage, origin string) {
		if len(raw) == 0 || string(raw) == "null" {
			return
		}
		var asString string
		if json.Unmarshal(raw, &asString) == nil {
			// A path to an .mcp.json-shaped file inside the plugin.
			data, err := readContainedFile(dir, strings.TrimPrefix(asString, "./"), remoteMarketplaceMaxJSON)
			if err != nil {
				detail.Warnings = append(detail.Warnings, fmt.Sprintf("%s: %v", origin, err))
				return
			}
			raw = data
		}
		var wrapped struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		var direct map[string]json.RawMessage
		if json.Unmarshal(raw, &wrapped) == nil && wrapped.MCPServers != nil {
			direct = wrapped.MCPServers
		} else if err := json.Unmarshal(raw, &direct); err != nil {
			detail.Warnings = append(detail.Warnings, fmt.Sprintf("%s: invalid mcpServers", origin))
			return
		}
		for name, cfg := range direct {
			if _, exists := servers[name]; !exists {
				servers[name] = cfg
			}
		}
	}
	if data, err := readContainedFile(dir, ".claude-plugin/plugin.json", remoteMarketplaceMaxJSON); err == nil {
		var manifest struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Version     string          `json:"version"`
			MCPServers  json.RawMessage `json:"mcpServers"`
		}
		if json.Unmarshal(data, &manifest) == nil {
			mergeMCP(manifest.MCPServers, "plugin.json")
		}
	} else {
		// Without plugin.json the marketplace entry is the manifest.
		mergeMCP(entry.MCPServers, "marketplace entry")
	}
	if data, err := readContainedFile(dir, ".mcp.json", remoteMarketplaceMaxJSON); err == nil {
		mergeMCP(data, ".mcp.json")
	}

	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		mcp, err := remoteMCPFromConfig(name, servers[name])
		if err != nil {
			detail.Warnings = append(detail.Warnings, fmt.Sprintf("MCP server %q: %v", name, err))
			continue
		}
		detail.MCPServers = append(detail.MCPServers, mcp)
	}
	return detail, packages, nil
}

var envPlaceholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-[^}]*)?\}`)

func remoteMCPFromConfig(name string, raw json.RawMessage) (RemotePluginMCP, error) {
	var cfg struct {
		Type    string            `json:"type"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return RemotePluginMCP{}, err
	}
	out := RemotePluginMCP{Name: name}
	vars := map[string]bool{}
	collect := func(v string) {
		for _, m := range envPlaceholder.FindAllStringSubmatch(v, -1) {
			if m[1] != "CLAUDE_PLUGIN_ROOT" && m[1] != "CLAUDE_PLUGIN_DATA" {
				vars[m[1]] = true
			}
		}
		if strings.Contains(v, "${CLAUDE_PLUGIN_ROOT}") || strings.Contains(v, "${CLAUDE_PLUGIN_DATA}") {
			out.PluginRelative = true
		}
	}
	switch {
	case cfg.URL != "":
		if cfg.Type != "" && cfg.Type != "http" && cfg.Type != "sse" && cfg.Type != "streamable-http" {
			return out, fmt.Errorf("unsupported type %q", cfg.Type)
		}
		out.Type = "http"
		out.Upstream = service.MCPUpstream{URL: cfg.URL, Headers: cfg.Headers}
		collect(cfg.URL)
		for _, v := range cfg.Headers {
			collect(v)
		}
	case cfg.Command != "":
		out.Type = "stdio"
		out.Upstream = service.MCPUpstream{Command: cfg.Command, Args: cfg.Args, Env: cfg.Env}
		collect(cfg.Command)
		for _, v := range cfg.Args {
			collect(v)
		}
		for _, v := range cfg.Env {
			collect(v)
		}
	default:
		return out, fmt.Errorf("neither url nor command")
	}
	for v := range vars {
		out.Variables = append(out.Variables, v)
	}
	sort.Strings(out.Variables)
	return out, nil
}

// applyMCPVariables replaces ${VAR} placeholders. A mapping value of
// "var:key" becomes {{var:key}} (resolved by AT at call time); anything else
// is inserted literally. Unmapped placeholders keep their ${VAR} default or
// are removed.
func applyMCPVariables(up service.MCPUpstream, mapping map[string]string) service.MCPUpstream {
	replace := func(v string) string {
		return envPlaceholder.ReplaceAllStringFunc(v, func(m string) string {
			sub := envPlaceholder.FindStringSubmatch(m)
			if val, ok := mapping[sub[1]]; ok {
				if key, isVar := strings.CutPrefix(val, "var:"); isVar {
					return "{{var:" + strings.TrimSpace(key) + "}}"
				}
				return val
			}
			if _, def, ok := strings.Cut(m, ":-"); ok {
				return strings.TrimSuffix(def, "}")
			}
			return ""
		})
	}
	up.URL = replace(up.URL)
	up.Command = replace(up.Command)
	if up.Args != nil {
		args := make([]string, len(up.Args))
		for i, a := range up.Args {
			args[i] = replace(a)
		}
		up.Args = args
	}
	mapReplace := func(in map[string]string) map[string]string {
		if in == nil {
			return nil
		}
		out := make(map[string]string, len(in))
		for k, v := range in {
			out[k] = replace(v)
		}
		return out
	}
	up.Headers = mapReplace(up.Headers)
	up.Env = mapReplace(up.Env)
	return up
}

// ─── File helpers ───

func containedDir(root, rel string) (string, error) {
	rel = strings.TrimSpace(strings.ReplaceAll(rel, "\\", "/"))
	clean := pathpkg.Clean(strings.TrimPrefix(rel, "./"))
	if clean == ".." || strings.HasPrefix(clean, "../") || pathpkg.IsAbs(clean) {
		return "", fmt.Errorf("path %q leaves the marketplace", rel)
	}
	dir := filepath.Join(root, filepath.FromSlash(clean))
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("open %q: %w", rel, err)
	}
	if resolved != dir {
		return "", fmt.Errorf("path %q may not contain symlinks", rel)
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", rel)
	}
	return dir, nil
}

// readContainedFile reads rel under root, refusing traversal and symlinks.
// A missing file returns an error wrapping os.ErrNotExist.
func readContainedFile(root, rel string, limit int64) ([]byte, error) {
	clean := pathpkg.Clean(strings.ReplaceAll(rel, "\\", "/"))
	if clean == ".." || strings.HasPrefix(clean, "../") || pathpkg.IsAbs(clean) {
		return nil, fmt.Errorf("path %q leaves the plugin", rel)
	}
	file := filepath.Join(root, filepath.FromSlash(clean))
	info, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%q is not a regular file", rel)
	}
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(file)); err != nil || resolved != filepath.Dir(file) {
		return nil, fmt.Errorf("%q may not be reached through a symlink", rel)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%q exceeds %d bytes", rel, limit)
	}
	return os.ReadFile(file)
}

func extractPluginZip(data []byte) (string, func(), error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", nil, fmt.Errorf("open archive: %w", err)
	}
	if len(zr.File) > remoteMarketplaceMaxFiles {
		return "", nil, fmt.Errorf("archive has more than %d entries", remoteMarketplaceMaxFiles)
	}
	tmp, err := os.MkdirTemp("", "at-plugin-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	root, err := filepath.EvalSymlinks(tmp)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	var total int64
	for _, f := range zr.File {
		name := pathpkg.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		if name == "." || name == ".." || strings.HasPrefix(name, "../") || pathpkg.IsAbs(name) {
			cleanup()
			return "", nil, fmt.Errorf("archive entry %q leaves the archive", f.Name)
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		mode := f.Mode()
		if mode.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				cleanup()
				return "", nil, err
			}
			continue
		}
		if !mode.IsRegular() {
			continue // symlinks and devices are skipped
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			cleanup()
			return "", nil, err
		}
		rc, err := f.Open()
		if err != nil {
			cleanup()
			return "", nil, err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			_ = rc.Close()
			cleanup()
			return "", nil, err
		}
		n, err := io.Copy(out, io.LimitReader(rc, remoteMarketplaceMaxArchive-total+1))
		_ = rc.Close()
		_ = out.Close()
		total += n
		if err != nil {
			cleanup()
			return "", nil, err
		}
		if total > remoteMarketplaceMaxArchive {
			cleanup()
			return "", nil, fmt.Errorf("archive expands beyond %d bytes", remoteMarketplaceMaxArchive)
		}
	}
	// The plugin root may be at the top or one directory down.
	entries, _ := os.ReadDir(root)
	if len(entries) == 1 && entries[0].IsDir() {
		if _, err := os.Stat(filepath.Join(root, ".claude-plugin")); errors.Is(err, os.ErrNotExist) {
			return filepath.Join(root, entries[0].Name()), cleanup, nil
		}
	}
	return root, cleanup, nil
}

// fetchPublicBytes downloads an https URL through the dialer that refuses
// private, loopback and link-local destinations (also on redirects).
func fetchPublicBytes(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("URL must be https")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := remoteMarketplaceClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response exceeds %d bytes", limit)
	}
	return data, nil
}

// remoteMarketplaceClient is replaceable in tests (httptest serves loopback).
var remoteMarketplaceClient = publicImageClient
