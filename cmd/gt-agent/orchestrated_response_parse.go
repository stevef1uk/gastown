package main

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

var (
	gluedNativeToolRE = regexp.MustCompile(`(?i)(>>>>>>>\s*REPLACE|---END\s+EDIT|---END\s+WRITE)\s*(CMD:|READ:|EDIT:|WRITE:)`)
	gluedCmdToToolRE  = regexp.MustCompile(`(?i)(CMD:\s*)(READ:|EDIT:|WRITE:)`)
	inlineToolRE      = regexp.MustCompile(`(?i)([^\s\n])(READ:|EDIT:|WRITE:)`)
	bdListLimitValueRE = regexp.MustCompile(`(?i)--limit(?:=|\s+)(\S+)`)
	bdListWordRE      = regexp.MustCompile(`(?i)\bbd\s+list\b`)
	bdBeadNumericIDRE = regexp.MustCompile(`^\d+$`)
)

// preprocessOrchestratedResponse normalizes glued CMD/READ/EDIT markers before parsing.
func preprocessOrchestratedResponse(response string) string {
	response = normalizeGluedCMDMarkers(response)
	response = gluedNativeToolRE.ReplaceAllString(response, "$1\n$2")
	response = gluedCmdToToolRE.ReplaceAllString(response, "$1\n$2")
	response = inlineToolRE.ReplaceAllString(response, "$1\n$2")
	response = unwrapMarkdownInlineToolLines(response)
	response = unwrapMarkdownFencedToolBlocks(response)
	response = stripMarkdownFenceOnlyLines(response)
	return response
}

// stripMarkdownFenceOnlyLines removes ``` / ```go lines models wrap around EDIT bodies.
func stripMarkdownFenceOnlyLines(response string) string {
	var out []string
	for _, line := range strings.Split(response, "\n") {
		t := strings.TrimSpace(line)
		if t == "```" || strings.HasPrefix(t, "```") && len(strings.TrimSpace(strings.TrimPrefix(t, "```"))) <= 8 {
			lang := strings.TrimSpace(strings.TrimPrefix(t, "```"))
			switch strings.ToLower(lang) {
			case "", "go", "golang", "python", "py", "bash", "sh", "shell", "text", "json":
				continue
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// stripOrchestratedShellBackticks removes markdown backticks that break /bin/sh (EOF in backquote substitution).
func stripOrchestratedShellBackticks(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	for i := 0; i < 6; i++ {
		changed := false
		if len(cmd) >= 2 && strings.HasPrefix(cmd, "`") && strings.HasSuffix(cmd, "`") {
			inner := strings.TrimSpace(cmd[1 : len(cmd)-1])
			if !strings.Contains(inner, "`") {
				cmd = inner
				changed = true
			}
		}
		if strings.HasPrefix(cmd, "`") {
			if j := strings.Index(cmd[1:], "`"); j >= 0 {
				cmd = strings.TrimSpace(cmd[1 : 1+j])
				changed = true
			} else {
				cmd = strings.TrimSpace(cmd[1:])
				changed = true
			}
		}
		if strings.HasSuffix(cmd, "`") {
			cmd = strings.TrimSpace(cmd[:len(cmd)-1])
			changed = true
		}
		if strings.HasPrefix(strings.ToUpper(cmd), "CMD:") {
			cmd = strings.TrimSpace(cmd[4:])
			changed = true
		}
		if !changed {
			break
		}
	}
	return strings.TrimSpace(cmd)
}

// unwrapMarkdownFencedToolBlocks unwraps ```go / ```python fences around CMD:/EDIT:/READ:/WRITE: blocks.
func unwrapMarkdownFencedToolBlocks(response string) string {
	lines := strings.Split(response, "\n")
	var out []string
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inFence {
			if strings.HasPrefix(trimmed, "```") {
				inner := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				upper := strings.ToUpper(inner)
				if strings.HasPrefix(upper, "CMD:") || strings.HasPrefix(upper, "READ:") ||
					strings.HasPrefix(upper, "EDIT:") || strings.HasPrefix(upper, "WRITE:") {
					out = append(out, inner)
					continue
				}
				if isMarkdownToolFenceLang(inner) {
					inFence = true
					continue
				}
			}
			out = append(out, line)
			continue
		}
		if trimmed == "```" {
			inFence = false
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func isMarkdownToolFenceLang(lang string) bool {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "", "go", "golang", "python", "py", "bash", "sh", "shell", "json", "text":
		return true
	default:
		return false
	}
}

func unwrapSingleBacktickLine(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && strings.HasPrefix(s, "`") && strings.HasSuffix(s, "`") {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	return ""
}

func looksLikeOrchestratedShellLine(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "{") {
		return false
	}
	lower := strings.ToLower(s)
	for _, p := range []string{"bd ", "export ", "cd ", "go ", "cat ", "bash ", "curl ", "chmod ", "ls "} {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return strings.Contains(lower, " && ") &&
		(strings.Contains(lower, "bd ") || strings.Contains(lower, "go "))
}

// FormatMalformedNativeEditFeedback reports EDIT: blocks missing <<<<<<< SEARCH (common model mistake).
func FormatMalformedNativeEditFeedback(response string) string {
	lines := strings.Split(response, "\n")
	var msgs []string
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		upper := strings.ToUpper(trimmed)
		if !strings.HasPrefix(upper, "EDIT:") {
			continue
		}
		path := strings.TrimSpace(trimmed[len("EDIT:"):])
		if path != "" && !orchestrator.IsValidImplementBeadPath(path) {
			msgs = append(msgs, "EDIT: rejected prose path "+strconv.Quote(path)+" — output a real path on its own line, e.g. EDIT: linkshelf/internal/api/handlers_test.go (no markdown fences or tutorial text on the EDIT: line)")
			continue
		}
		hasSearch := false
		for j := i + 1; j < len(lines) && j < i+40; j++ {
			t := strings.TrimSpace(lines[j])
			tu := strings.ToUpper(t)
			if strings.HasPrefix(tu, "EDIT:") || strings.HasPrefix(tu, "WRITE:") ||
				strings.HasPrefix(tu, "READ:") || strings.HasPrefix(tu, "CMD:") {
				break
			}
			if t == nativeEditSearchMarker || strings.HasPrefix(t, "<<<<<<<") {
				hasSearch = true
				break
			}
			if t == nativeEditReplaceMarker || strings.HasPrefix(t, ">>>>>>>") ||
				isNativeEditEndMarker(t) {
				break
			}
		}
		if !hasSearch {
			msgs = append(msgs, "EDIT: "+path+" is missing "+nativeEditSearchMarker+" / ======= before >>>>>>> REPLACE — copy from ### Current file on disk, then retry.")
		}
	}
	if len(msgs) == 0 {
		return ""
	}
	return strings.TrimSpace("Malformed EDIT (not applied):\n- " + strings.Join(msgs, "\n- "))
}

// unwrapMarkdownInlineToolLines strips `CMD:` / `EDIT:` lines wrapped in markdown backticks.
func unwrapMarkdownInlineToolLines(response string) string {
	var out []string
	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(upper, "CMD:") || strings.HasPrefix(upper, "READ:") ||
			strings.HasPrefix(upper, "EDIT:") || strings.HasPrefix(upper, "WRITE:") {
			out = append(out, line)
			continue
		}
		if inner := unwrapSingleBacktickLine(trimmed); inner != "" && looksLikeOrchestratedShellLine(inner) {
			if !strings.HasPrefix(strings.ToUpper(inner), "CMD:") {
				out = append(out, "CMD: "+inner)
			} else {
				out = append(out, inner)
			}
			continue
		}
		unwrapped := unwrapMarkdownInlineCode(trimmed)
		u := strings.ToUpper(strings.TrimSpace(unwrapped))
		if strings.HasPrefix(u, "CMD:") || strings.HasPrefix(u, "READ:") ||
			strings.HasPrefix(u, "EDIT:") || strings.HasPrefix(u, "WRITE:") {
			// Preserve leading whitespace on the original line when present.
			if i := strings.Index(line, trimmed); i >= 0 {
				out = append(out, line[:i]+unwrapped)
			} else {
				out = append(out, unwrapped)
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func unwrapMarkdownInlineCode(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && strings.HasPrefix(s, "`") && strings.HasSuffix(s, "`") {
		inner := strings.TrimSpace(s[1 : len(s)-1])
		if strings.HasPrefix(inner, "`") && strings.HasSuffix(inner, "`") && len(inner) >= 2 {
			inner = strings.TrimSpace(inner[1 : len(inner)-1])
		}
		return inner
	}
	// Trailing backtick from `CMD: ...` prose (common model mistake).
	if strings.HasPrefix(strings.ToUpper(s), "CMD:") || strings.HasPrefix(strings.ToUpper(s), "EDIT:") ||
		strings.HasPrefix(strings.ToUpper(s), "WRITE:") || strings.HasPrefix(strings.ToUpper(s), "READ:") {
		return strings.TrimRight(s, "`")
	}
	return s
}

// sanitizeOrchestratedShellCommand trims model prose/JSON glued onto shell commands.
func sanitizeOrchestratedShellCommand(cmd string) (string, bool) {
	changed := false
	if stripped := stripOrchestratedShellBackticks(cmd); stripped != cmd {
		cmd = stripped
		changed = true
	}
	if fixed, ok := trimJSONGluedToShellCommand(cmd); ok {
		cmd = fixed
		changed = true
	}
	if fixed, ok := trimProseGluedAfterGoTestEllipsis(cmd); ok {
		cmd = fixed
		changed = true
	}
	if fixed, ok := trimProseGluedAfterGoBuildEllipsis(cmd); ok {
		cmd = fixed
		changed = true
	}
	// bd list --limit fixes stay in rewriteBdListLimit (CmdRewrites), not here — injecting
	// --limit=0 during parse broke tests and changes commands that already omit --limit by design.
	return strings.TrimSpace(cmd), changed
}

func trimJSONGluedToShellCommand(cmd string) (string, bool) {
	for _, needle := range []string{`{"outcome"`, `{" outcome"`} {
		if i := strings.Index(cmd, needle); i > 0 {
			return strings.TrimSpace(cmd[:i]), true
		}
	}
	if i := strings.Index(cmd, "{"); i > 0 {
		rest := strings.TrimSpace(cmd[i:])
		if strings.HasPrefix(rest, "{") && (strings.Contains(rest, `"outcome"`) || strings.Contains(rest, `"summary"`)) {
			return strings.TrimSpace(cmd[:i]), true
		}
	}
	return cmd, false
}

func trimProseGluedAfterGoTestEllipsis(cmd string) (string, bool) {
	return trimProseGluedAfterGoSubcommandEllipsis(cmd, "go test")
}

func trimProseGluedAfterGoBuildEllipsis(cmd string) (string, bool) {
	return trimProseGluedAfterGoSubcommandEllipsis(cmd, "go build")
}

func trimProseGluedAfterGoSubcommandEllipsis(cmd, subcmd string) (string, bool) {
	lower := strings.ToLower(cmd)
	idx := strings.Index(lower, subcmd)
	if idx < 0 {
		return cmd, false
	}
	segment := cmd[idx:]
	dot := strings.Index(segment, "...")
	if dot < 0 {
		return cmd, false
	}
	end := idx + dot + 3
	if end >= len(cmd) {
		return cmd, false
	}
	r := cmd[end]
	if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
		return strings.TrimSpace(cmd[:end]), true
	}
	return cmd, false
}

// sanitizeBdListCommand fixes --limit glued with prose and ensures --limit=0 when missing.
func sanitizeBdListCommand(cmd string) (string, bool) {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "bd list") {
		return cmd, false
	}
	changed := false
	if m := bdListLimitValueRE.FindStringSubmatch(cmd); len(m) >= 2 {
		val := strings.TrimSpace(m[1])
		digits := limitDigitsPrefix(val)
		if digits != "" && digits != val {
			cmd = strings.Replace(cmd, m[0], "--limit="+digits, 1)
			changed = true
		}
	}
	if fixed, ok := fixBdListGluedToWord(cmd); ok {
		cmd = fixed
		changed = true
	}
	if !strings.Contains(strings.ToLower(cmd), "--limit") {
		cmd = bdListWordRE.ReplaceAllString(cmd, "bd list --limit=0")
		changed = true
	}
	return cmd, changed
}

func fixBdListGluedToWord(cmd string) (string, bool) {
	lower := strings.ToLower(cmd)
	idx := strings.Index(lower, "bd list")
	if idx < 0 {
		return cmd, false
	}
	rest := cmd[idx+len("bd list"):]
	if rest == "" || rest[0] == ' ' || rest[0] == '-' {
		return cmd, false
	}
	return cmd[:idx+len("bd list")] + " --limit=0 " + strings.TrimSpace(rest), true
}

func limitDigitsPrefix(val string) string {
	var b strings.Builder
	for _, c := range val {
		if c >= '0' && c <= '9' {
			b.WriteRune(c)
		} else {
			break
		}
	}
	return b.String()
}

func isBdInfrastructureFailure(cmdErr error, output string) bool {
	text := strings.ToLower(strings.TrimSpace(output))
	if cmdErr != nil {
		text += " " + strings.ToLower(cmdErr.Error())
	}
	return strings.Contains(text, "failed to open database") ||
		(strings.Contains(text, "database ") && strings.Contains(text, "not found on dolt"))
}

func isNativeEditEndMarker(line string) bool {
	t := strings.TrimSpace(line)
	return t == nativeEditEndMarker || strings.EqualFold(t, "---END EDIT---")
}

// isOrchestratedNativeToolLine reports READ:/EDIT:/WRITE: markers that must never run as shell.
func isOrchestratedNativeToolLine(line string) bool {
	t := strings.TrimSpace(line)
	upper := strings.ToUpper(t)
	return strings.HasPrefix(upper, "READ:") ||
		strings.HasPrefix(upper, "EDIT:") ||
		strings.HasPrefix(upper, "WRITE:")
}

// stripNativeToolBlocksForCmdParse removes native tool blocks so WRITE/EDIT bodies are not executed as shell.
func stripNativeToolBlocksForCmdParse(response string) string {
	lines := strings.Split(response, "\n")
	var kept []string
	skip := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		upper := strings.ToUpper(t)
		if isOrchestratedNativeToolLine(line) || strings.HasPrefix(upper, "<<<<<<<") {
			skip = true
			continue
		}
		if skip {
			if isStrayFileTerminatorLine(t) || isNativeEditEndMarker(t) {
				skip = false
				continue
			}
			if strings.HasPrefix(upper, "CMD:") {
				skip = false
				kept = append(kept, line)
			}
			continue
		}
		if t == "=======" || strings.HasPrefix(upper, ">>>>>>>") {
			skip = true
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// sanitizeNativeFileContent strips markdown fences and stray heredoc/WRITE terminators
// the model often appends to EDIT/WRITE bodies or heredoc file content.
func sanitizeNativeFileContent(content string) string {
	if content == "" {
		return content
	}
	hadTrailingNL := strings.HasSuffix(content, "\n")
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	changed := false
	for {
		trimChanged := false
		for len(lines) > 0 {
			first := strings.TrimSpace(lines[0])
			if strings.HasPrefix(first, "```") || isNativeEditArtifactLine(first) {
				lines = lines[1:]
				trimChanged = true
				continue
			}
			break
		}
		for len(lines) > 0 {
			last := strings.TrimSpace(lines[len(lines)-1])
			if last == "```" || isStrayFileTerminatorLine(last) || isNativeEditArtifactLine(last) {
				lines = lines[:len(lines)-1]
				trimChanged = true
				continue
			}
			break
		}
		if !trimChanged {
			break
		}
		changed = true
	}
	if !changed {
		return content
	}
	out := strings.Join(lines, "\n")
	if hadTrailingNL && len(lines) > 0 {
		out += "\n"
	}
	return out
}

// stripMarkdownCodeFencesFromSource removes outer ```lang / ``` wrappers (one pass).
func stripMarkdownCodeFencesFromSource(content string) string {
	return sanitizeNativeFileContent(content)
}

func isStrayFileTerminatorLine(line string) bool {
	switch strings.ToUpper(strings.TrimSpace(line)) {
	case "EOF", "EOT", "END":
		return true
	}
	t := strings.TrimSpace(line)
	return strings.EqualFold(t, nativeEditWriteEnd) ||
		strings.EqualFold(t, "---END EDIT---") ||
		strings.EqualFold(t, "---END WRITE---")
}

// isNativeEditArtifactLine reports EDIT/WRITE marker lines the model glued into file bodies
// (e.g. "<<<<<<< EOF" from heredoc confusion, or "<<<<<<< SEARCH" before package).
func isNativeEditArtifactLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	if t == nativeEditSearchMarker || t == nativeEditReplaceMarker || t == nativeEditEndMarker {
		return true
	}
	if strings.HasPrefix(strings.ToUpper(t), "<<<<<<<") || strings.HasPrefix(t, ">>>>>>>") {
		return true
	}
	return t == "======="
}
