package nodes

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/wneessen/go-mail"

	"github.com/rakunlabs/at/internal/service"
)

// emailAttachment is one resolved file ready to be attached.
type emailAttachment struct {
	name        string
	contentType string
	content     []byte
}

// attachmentSpec is one requested attachment before its bytes are loaded:
// either a stored file (workspacePath) or inline base64 content.
type attachmentSpec struct {
	workspacePath string
	name          string
	contentType   string
	inline        []byte
	hasInline     bool
}

// parseAttachmentField splits the rendered attachments field into run
// workspace paths. Newlines and commas separate entries.
func parseAttachmentField(runDir, rendered string) ([]attachmentSpec, error) {
	var specs []attachmentSpec
	for _, line := range strings.FieldsFunc(rendered, func(r rune) bool { return r == '\n' || r == ',' }) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rel, err := cleanRunPath(line)
		if err != nil {
			return nil, err
		}
		specs = append(specs, attachmentSpec{workspacePath: path.Join(runDir, rel), name: path.Base(rel)})
	}
	return specs, nil
}

// parseAttachmentInput accepts what upstream nodes naturally produce:
//   - a path string, or a list of them
//   - a file reference {path|workspace_path, name?, content_type?}
//     (the "file" output of HTTP Request, or any node that emits the shape)
//   - an inline object {name, content_base64, content_type?} from a Script node
//   - an object wrapping one of the above under "file" or "attachments"
//   - a list mixing any of these
func parseAttachmentInput(runDir string, value any) ([]attachmentSpec, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case string:
		return parseAttachmentField(runDir, v)
	case []any:
		var specs []attachmentSpec
		for i, item := range v {
			parsed, err := parseAttachmentInput(runDir, item)
			if err != nil {
				return nil, fmt.Errorf("attachment %d: %w", i+1, err)
			}
			specs = append(specs, parsed...)
		}
		return specs, nil
	case []string:
		items := make([]any, len(v))
		for i := range v {
			items[i] = v[i]
		}
		return parseAttachmentInput(runDir, items)
	case map[string]any:
		return parseAttachmentObject(runDir, v)
	default:
		return nil, fmt.Errorf("unsupported attachment value of type %T", value)
	}
}

func parseAttachmentObject(runDir string, v map[string]any) ([]attachmentSpec, error) {
	name, _ := v["name"].(string)
	name = safeFileName(name)
	contentType, _ := v["content_type"].(string)

	if encoded, ok := v["content_base64"].(string); ok {
		if name == "" {
			return nil, fmt.Errorf("inline attachment requires a name")
		}
		content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
		if err != nil {
			return nil, fmt.Errorf("inline attachment %q: invalid base64: %w", name, err)
		}
		return []attachmentSpec{{name: name, contentType: contentType, inline: content, hasInline: true}}, nil
	}

	if workspacePath, ok := v["workspace_path"].(string); ok && strings.TrimSpace(workspacePath) != "" {
		cleaned, err := cleanRunPath(workspacePath)
		if err != nil {
			return nil, err
		}
		if name == "" {
			name = path.Base(cleaned)
		}
		return []attachmentSpec{{workspacePath: cleaned, name: name, contentType: contentType}}, nil
	}

	if p, ok := v["path"].(string); ok && strings.TrimSpace(p) != "" {
		rel, err := cleanRunPath(p)
		if err != nil {
			return nil, err
		}
		if name == "" {
			name = path.Base(rel)
		}
		return []attachmentSpec{{workspacePath: path.Join(runDir, rel), name: name, contentType: contentType}}, nil
	}

	for _, key := range []string{"file", "attachments"} {
		if nested, ok := v[key]; ok {
			return parseAttachmentInput(runDir, nested)
		}
	}

	return nil, fmt.Errorf("attachment object needs path, workspace_path or content_base64")
}

// loadAttachments reads every requested file through the execution root, so
// containment and file admission match the file tools. Count and total size
// are bounded because the whole message is assembled in memory.
func loadAttachments(ctx context.Context, specs []attachmentSpec) ([]emailAttachment, error) {
	if len(specs) > maxAttachmentCount {
		return nil, fmt.Errorf("too many attachments: %d (maximum %d)", len(specs), maxAttachmentCount)
	}
	var total int64
	out := make([]emailAttachment, 0, len(specs))
	for _, spec := range specs {
		content := spec.inline
		if !spec.hasInline {
			var err error
			content, err = readWorkspaceFile(ctx, spec.workspacePath, maxAttachmentTotalBytes-total)
			if err != nil {
				return nil, fmt.Errorf("attachment %q: %w", spec.name, err)
			}
		}
		total += int64(len(content))
		if total > maxAttachmentTotalBytes {
			return nil, fmt.Errorf("attachments exceed %d bytes in total", maxAttachmentTotalBytes)
		}
		out = append(out, emailAttachment{
			name:        spec.name,
			contentType: attachmentContentType(spec.contentType, spec.name, content),
			content:     content,
		})
	}
	return out, nil
}

func readWorkspaceFile(ctx context.Context, workspacePath string, limit int64) ([]byte, error) {
	root, name, err := service.OpenExecutionRoot(ctx, workspacePath, false)
	if err != nil {
		return nil, err
	}
	defer root.Close()

	f, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", workspacePath)
	}
	if limit < 0 {
		limit = 0
	}
	content, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("attachments exceed %d bytes in total", maxAttachmentTotalBytes)
	}
	return content, nil
}

func attachToMessage(m *mail.Msg, attachments []emailAttachment) error {
	for _, a := range attachments {
		if err := m.AttachReader(a.name, bytes.NewReader(a.content), mail.WithFileContentType(mail.ContentType(a.contentType))); err != nil {
			return fmt.Errorf("attach %q: %w", a.name, err)
		}
	}
	return nil
}
