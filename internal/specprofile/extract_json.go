package specprofile

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractJSONObject finds and unmarshals the first top-level JSON object in s.
// It uses a small FSM that scans for balanced { } braces while respecting string
// literals and escape sequences, so reasoning tags, markdown fences, prose, and
// multiple/adjacent JSON objects don't confuse extraction.
func ExtractJSONObject(s string, out any) error {
	candidates := findJSONObjects(s)
	if len(candidates) == 0 {
		return fmt.Errorf("no JSON object found in response (response: %.300s)", strings.TrimSpace(s))
	}
	// Prefer the LAST complete object (model output usually ends with the real JSON),
	// then fall back to earlier ones.
	for i := len(candidates) - 1; i >= 0; i-- {
		if err := json.Unmarshal([]byte(candidates[i]), out); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no parseable JSON object in response (response: %.300s)", strings.TrimSpace(s))
}

// findJSONObjects scans s and returns every balanced top-level { } object,
// ignoring braces inside string literals and escape sequences.
func findJSONObjects(s string) []string {
	var objs []string
	var buf []rune
	var start, depth int
	inString := false
	escaped := false
	haveStart := false

	flush := func(end int) {
		if !haveStart {
			return
		}
		obj := strings.TrimSpace(string(buf))
		if obj != "" {
			objs = append(objs, obj)
		}
		_ = end
	}

	for _, r := range s {
		if inString {
			buf = append(buf, r)
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == '"' {
				inString = false
			}
			continue
		}
		switch r {
		case '"':
			inString = true
			if haveStart {
				buf = append(buf, r)
			}
		case '{':
			if depth == 0 {
				haveStart = true
				buf = buf[:0]
				start = 0
			}
			depth++
			if haveStart {
				buf = append(buf, r)
			}
		case '}':
			if depth > 0 {
				depth--
				if haveStart {
					buf = append(buf, r)
				}
				if depth == 0 {
					flush(start)
					haveStart = false
				}
			}
		default:
			if haveStart {
				buf = append(buf, r)
			}
		}
	}
	return objs
}
