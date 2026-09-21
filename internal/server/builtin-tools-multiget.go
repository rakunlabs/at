package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// multiGetLimit bounds how many records a single "get" call may resolve. It is
// the ceiling batch_execute already uses: a model that pastes a whole list of
// identifiers should get a bounded answer and an explicit instruction, not an
// unbounded fan-out of store reads whose combined output the loop governor
// would truncate anyway.
const multiGetLimit = 25

// multiGetMaxDepth bounds the walk over a supplied identifier value. The value
// is model-generated, so its nesting is not a property this code controls.
const multiGetMaxDepth = 4

// multiGetIDs reads an identifier argument that is documented as one value but
// is routinely supplied as a list. Models fetching several records in one call
// sent an array (or a separated string, or the plural key) into an
// `args[key].(string)` read, which yielded an empty identifier and then the
// misleading error "id is required" — the argument was present, it just had
// the other shape.
//
// Accepted in addition to a plain string:
//
//	["a","b"]           array of identifiers
//	"a,b" / "a\nb"      separated list
//	[{"id":"a"}, …]     array of objects carrying the key
//	args["ids"]         plural alias of args["id"]
//
// Splitting is on commas and newlines only, never on interior spaces: some of
// these arguments accept a name where an identifier is expected, and names
// contain spaces. Values are trimmed, empties dropped and duplicates removed
// with first-seen order preserved, because the reply lists results in the
// order they were asked for.
func multiGetIDs(args map[string]any, key string) []string {
	raw, ok := args[key]
	if !ok || raw == nil {
		raw = args[key+"s"]
	}

	var out []string
	seen := make(map[string]struct{})
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		if _, dup := seen[v]; dup {
			return
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}

	var collect func(value any, depth int)
	collect = func(value any, depth int) {
		if depth > multiGetMaxDepth {
			return
		}
		switch v := value.(type) {
		case string:
			for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
				add(part)
			}
		case []string:
			for _, item := range v {
				collect(item, depth+1)
			}
		case []any:
			for _, item := range v {
				collect(item, depth+1)
			}
		case map[string]any:
			// `[{"id": "…"}]` and `{"id": "…"}` are both shapes models produce
			// when they mirror the result envelope back into a request.
			if nested, ok := v[key]; ok {
				collect(nested, depth+1)
			}
		}
	}
	collect(raw, 0)

	return out
}

// multiGet resolves one or more records with the supplied single-record
// fetcher.
//
// One identifier keeps the historical response byte-for-byte: the fetcher's
// own JSON, and its own error when the record is missing. Several identifiers
// produce a result envelope in which a failing entry is reported per item
// rather than failing the call — one unknown identifier in a list of five must
// not discard the four that resolved, since the model cannot tell which one
// was at fault from a single error string.
func multiGet(ctx context.Context, args map[string]any, key string, one func(context.Context, string) (string, error)) (string, error) {
	ids := multiGetIDs(args, key)
	switch {
	case len(ids) == 0:
		return "", fmt.Errorf("%s is required (a single value, or an array of values to fetch several at once)", key)
	case len(ids) == 1:
		return one(ctx, ids[0])
	case len(ids) > multiGetLimit:
		return "", fmt.Errorf("too many values for %s: %d requested, maximum %d per call — split the list across several calls", key, len(ids), multiGetLimit)
	}

	results := make([]map[string]any, 0, len(ids))
	found := 0
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		body, err := one(ctx, id)
		if err != nil {
			results = append(results, map[string]any{key: id, "ok": false, "error": err.Error()})
			continue
		}
		found++
		results = append(results, map[string]any{key: id, "ok": true, "data": jsonOrString(body)})
	}

	data, err := json.MarshalIndent(map[string]any{
		"requested": len(ids),
		"found":     found,
		"failed":    len(ids) - found,
		"results":   results,
	}, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal %s results: %w", key, err)
	}
	return string(data), nil
}

// jsonOrString embeds an executor's output in the envelope as structured JSON
// when it is JSON, and as a plain string otherwise, so nesting never re-encodes
// a document into an escaped string the model has to parse twice.
func jsonOrString(body string) any {
	if json.Valid([]byte(body)) {
		return json.RawMessage(body)
	}
	return body
}
