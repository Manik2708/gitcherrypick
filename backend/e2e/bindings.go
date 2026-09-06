package e2e

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Bindings are the values a fixture step may reference as {{name}}.
//
// They come from three places, in the order a case encounters them: seed data
// ({{alice.id}}, {{pending_pat.verification_id}}), an explicit `capture` block,
// and the automatic rules in fixtures/README.md. This lives outside the test
// file because the database assertions substitute them too — a `where` clause
// naming {{alice.id}} has to resolve the same way a path does.

var placeholderRe = regexp.MustCompile(`\{\{([a-z0-9_.]+)\}\}`)

// resolve substitutes {{name}} bindings into a path.
func resolve(s string, bindings map[string]string) (string, error) {
	var missing []string
	out := placeholderRe.ReplaceAllStringFunc(s, func(match string) string {
		key := match[2 : len(match)-2]
		if v, ok := bindings[key]; ok {
			return v
		}
		missing = append(missing, key)
		return match
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("unresolved placeholder(s) %v — no earlier step bound them", missing)
	}
	return out, nil
}

func resolveJSON(raw json.RawMessage, bindings map[string]string) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	resolved, err := resolve(string(raw), bindings)
	if err != nil {
		return nil, err
	}
	return []byte(resolved), nil
}

// bind records values a later step can reference: explicit captures, plus the
// automatic rules documented in fixtures/README.md.
func bind(bindings map[string]string, step Step, respBody []byte) {
	var body any
	if err := json.Unmarshal(respBody, &body); err != nil {
		return
	}
	obj, ok := body.(map[string]any)
	if !ok {
		return
	}

	for name, path := range step.Capture {
		if v, ok := lookup(obj, strings.TrimPrefix(path, "$.")); ok {
			bindings[name] = v
		}
	}

	// Any *_id key, at any depth.
	var walk func(node any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			for k, val := range v {
				if strings.HasSuffix(k, "_id") {
					if s, ok := val.(string); ok {
						bindings[k] = s
					}
				}
				// A nested object with an id binds <key>_id.
				if nested, ok := val.(map[string]any); ok {
					if id, ok := nested["id"].(string); ok {
						bindings[k+"_id"] = id
					}
				}
				walk(val)
			}
		case []any:
			for _, item := range v {
				walk(item)
			}
		}
	}
	walk(obj)

	// `POST /claims -> {"id": ...}` binds {{claim_id}}.
	if id, ok := obj["id"].(string); ok {
		if seg := firstSegment(step.Request.Path); seg != "" {
			bindings[seg+"_id"] = id
		}
	}
}

func firstSegment(path string) string {
	trimmed := strings.TrimPrefix(path, "/")
	if i := strings.IndexAny(trimmed, "/?"); i >= 0 {
		trimmed = trimmed[:i]
	}
	if trimmed == "" {
		return ""
	}
	return strings.ReplaceAll(strings.TrimSuffix(trimmed, "s"), "-", "_")
}

func lookup(obj map[string]any, path string) (string, bool) {
	var node any = obj
	for _, part := range strings.Split(path, ".") {
		key, index, indexed := splitIndex(part)

		m, ok := node.(map[string]any)
		if !ok {
			return "", false
		}
		node, ok = m[key]
		if !ok {
			return "", false
		}

		if indexed {
			list, ok := node.([]any)
			if !ok || index >= len(list) {
				return "", false
			}
			node = list[index]
		}
	}
	s, ok := node.(string)
	return s, ok
}

// splitIndex reads `suggestions[0]` as the key and the position within it.
//
// A capture path into a list is the only way to name one of several items a
// response returns in order — two AI suggestions, say, where neither has a
// key the fixture could otherwise address it by.
func splitIndex(part string) (string, int, bool) {
	open := strings.IndexByte(part, '[')
	if open < 0 || !strings.HasSuffix(part, "]") {
		return part, 0, false
	}
	index, err := strconv.Atoi(part[open+1 : len(part)-1])
	if err != nil || index < 0 {
		return part, 0, false
	}
	return part[:open], index, true
}
