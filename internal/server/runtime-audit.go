package server

import (
	"bytes"
	"os"
	"path/filepath"
	"time"

	"github.com/rakunlabs/at/internal/service"
)

// Audit append is part of an already-admitted action, not a new user file-write
// capability. Preserve its initiating workspace even if cancellation/revocation
// happens between the upstream side effect and recording its result.
func (s *Server) spillExecutionPayload(base, id, side string, body []byte) string {
	root, err := os.OpenRoot(base)
	if err != nil {
		return ""
	}
	defer root.Close()
	name := filepath.Join(llmAuditDumpDir, time.Now().UTC().Format("2006-01-02"), id+"-"+side+".json")
	if !filepath.IsLocal(name) {
		return ""
	}
	if err := service.MkdirExecutionAll(root, filepath.Dir(name), 0700); err != nil {
		return ""
	}
	if _, err := service.ReplaceExecutionFile(root, name, bytes.NewReader(body)); err != nil {
		return ""
	}
	return filepath.Join(base, name)
}
