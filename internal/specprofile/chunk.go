package specprofile

import (
	"strings"
)

const specChunkTarget = 28000

// splitSpecIntoChunks splits a large SPEC on markdown ## headings for multi-pass LLM indexing.
func splitSpecIntoChunks(spec string) []string {
	spec = strings.TrimSpace(spec)
	if len(spec) <= maxSpecChars {
		return []string{spec}
	}
	lines := strings.Split(spec, "\n")
	var chunks []string
	var cur strings.Builder
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") && cur.Len() > specChunkTarget {
			chunks = append(chunks, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
		cur.WriteString(line)
		cur.WriteByte('\n')
	}
	if tail := strings.TrimSpace(cur.String()); tail != "" {
		chunks = append(chunks, tail)
	}
	if len(chunks) == 0 {
		return []string{spec[:maxSpecChars]}
	}
	return chunks
}
