package main

import (
	"regexp"
	"strings"
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
	return response
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
			if strings.HasPrefix(first, "```") {
				lines = lines[1:]
				trimChanged = true
				continue
			}
			break
		}
		for len(lines) > 0 {
			last := strings.TrimSpace(lines[len(lines)-1])
			if last == "```" || isStrayFileTerminatorLine(last) {
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
