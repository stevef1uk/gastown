package specprofile

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ExtractJSONObject finds and unmarshals the first top-level JSON object in s (strips markdown fences).
func ExtractJSONObject(s string, out any) error {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = stripMarkdownFence(s)
	}
	// Strip any remaining prose before the first { or after the last }
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		// Try to unmarshal the whole string as-is (may be raw JSON without braces for array)
		if err := json.Unmarshal([]byte(s), out); err != nil {
			return fmt.Errorf("no JSON object found in response: %w (response: %.300s)", err, s)
		}
		return nil
	}
	return json.Unmarshal([]byte(s[start:end+1]), out)
}

func stripMarkdownFence(s string) string {
	lines := strings.Split(s, "\n")
	var buf []string
	inFence := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence || (!strings.HasPrefix(t, "```") && len(buf) > 0) {
			buf = append(buf, line)
		}
	}
	if len(buf) == 0 {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(strings.Join(buf, "\n"))
}
