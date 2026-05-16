package specprofile

import (
	"encoding/json"
	"strings"
)

// ExtractJSONObject finds and unmarshals the first top-level JSON object in s (strips markdown fences).
func ExtractJSONObject(s string, out any) error {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = stripMarkdownFence(s)
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return json.Unmarshal([]byte(s), out)
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
