package service

import "regexp"

// Stored media can be referenced from message text as a Markdown target
// `media:<id>` (e.g. `![chart](media:01J...)`). The browser resolves the
// reference to the caller's workspace-scoped media URL at render time, so a
// model never has to know — or guess — that URL.

const MediaRefScheme = "media:"

var mediaRefPattern = regexp.MustCompile(`\]\(\s*<?media:([0-9A-Za-z_-]{1,128})`)

// MediaRefMarkdown is the Markdown a model should place in its answer to show
// a stored object: an image for inline image types, a link otherwise.
func MediaRefMarkdown(id, name string, image bool) string {
	label := markdownLabel(name)
	if image {
		return "![" + label + "](" + MediaRefScheme + id + ")"
	}
	return "[" + label + "](" + MediaRefScheme + id + ")"
}

// ReplaceMediaRefs rewrites `](media:<old>` targets in text using
// replacements, leaving unknown IDs untouched. Used when media objects are
// copied (chat shares and their import), where the stored parts' media_id
// fields are rewritten and the text references must follow them.
func ReplaceMediaRefs(text string, replacements map[string]string) string {
	if len(replacements) == 0 {
		return text
	}
	return mediaRefPattern.ReplaceAllStringFunc(text, func(match string) string {
		sub := mediaRefPattern.FindStringSubmatch(match)
		if replacement := replacements[sub[1]]; replacement != "" {
			return match[:len(match)-len(sub[1])] + replacement
		}
		return match
	})
}

func markdownLabel(name string) string {
	out := make([]rune, 0, len(name))
	for _, r := range name {
		switch r {
		case '[', ']':
			continue
		case '\n', '\r':
			r = ' '
		}
		out = append(out, r)
	}
	return string(out)
}
