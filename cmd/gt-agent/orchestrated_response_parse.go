package main

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

var (
	gluedNativeToolRE = regexp.MustCompile(`(?i)(>>>>>>>\s*REPLACE|---END\s+EDIT|---END\s+WRITE)\s*(CMD:|READ:|EDIT:|WRITE:)`)
	gluedCmdToToolRE  = regexp.MustCompile(`(?i)(CMD:\s*)(READ:|EDIT:|WRITE:)`)
	cmdGluedToToolRE  = regexp.MustCompile(`(?i)(CMD:\s+.+?)\s+(EDIT:|WRITE:|READ:)`)
	inlineToolRE      = regexp.MustCompile(`(?i)([^\s\n])(READ:|EDIT:|WRITE:)`)
	bdListLimitValueRE   = regexp.MustCompile(`(?i)--limit(?:=|\s+)(\S+)`)
	bdListWordRE         = regexp.MustCompile(`(?i)\bbd\s+list\b`)
	bdListStatusGluedRE  = regexp.MustCompile(`(?i)--status=([a-z_,]+)\([^)]*\)`)
	bdBeadNumericIDRE = regexp.MustCompile(`^\d+$`)
)

// preprocessOrchestratedResponse normalizes glued CMD/READ/EDIT markers before parsing.
func preprocessOrchestratedResponse(response string) string {
	response = stripChannelMarkers(response)
	response = unwrapJSONOrchestratedCommands(response)
	response = unwrapJSONCommandArray(response)
	response = unwrapJSONActionCommands(response)
	response = unwrapFunctionToolCalls(response)
	response = normalizeGluedWriteBody(response)
	response = unwrapAngleBracketCMD(response)
	response = normalizeGluedCMDMarkers(response)
	response = gluedNativeToolRE.ReplaceAllString(response, "$1\n$2")
	response = gluedCmdToToolRE.ReplaceAllString(response, "$1\n$2")
	response = cmdGluedToToolRE.ReplaceAllString(response, "$1\n$2")
	response = inlineToolRE.ReplaceAllString(response, "$1\n$2")
	response = unwrapMarkdownInlineToolLines(response)
	response = unwrapMarkdownBoldToolLines(response)
	response = unwrapMarkdownFencedToolBlocks(response)
	response = stripMarkdownFenceOnlyLines(response)
	response = unwrapJSONToolCallCommand(response)
	response = unwrapDSMLToolCalls(response)
	response = normalizeNativeEditEndLines(response)
	return response
}

// stripChannelMarkers removes LLM channel/role markers like <|message|>, <|start|>,
// <|channel|>analysis that some models emit inline with CMD/EDIT/READ lines.
// Each tag is replaced with \n\n (blank line) so that the script accumulator in
// parseLLMResponse flushes the current command and ignores the prose that follows.
// The LLM often emits the entire response as ONE physical line (no \n), using
// channel markers as separators between commands and internal reasoning prose.
//
// Without replacement:
//   --limit=0proseCMD: next --flag    (unknown flag, shell error)
// With \n (single newline):
//   --limit=0\nprose                  (inScript=true slurps "prose" into cmd)
// With \n\n (blank line, this):
//   --limit=0\n\nprose                (blank line flushes script, prose ignored)
var channelMarkerRE = regexp.MustCompile(`<\|[^|]+\|>`)

func stripChannelMarkers(s string) string {
	return channelMarkerRE.ReplaceAllString(s, "\n\n")
}

// unwrapJSONOrchestratedCommands converts {"CMD":"..."} / {"cmd":"..."} lines local LLMs emit into CMD: lines.
func unwrapJSONOrchestratedCommands(response string) string {
	var out []string
	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "{") {
			out = append(out, line)
			continue
		}
		if cmd := jsonOrchestratedCommand(trimmed); cmd != "" {
			out = append(out, "CMD: "+cmd)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func jsonOrchestratedCommand(line string) string {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return ""
	}
	for _, key := range []string{"CMD", "cmd", "Cmd"} {
		raw, ok := obj[key]
		if !ok || len(raw) == 0 {
			continue
		}
		var cmd string
		if err := json.Unmarshal(raw, &cmd); err != nil || strings.TrimSpace(cmd) == "" {
			continue
		}
		return strings.TrimSpace(cmd)
	}
	return ""
}

// unwrapJSONActionCommands converts {"action":"CMD","command":"..."} / {"action":"READ","path":"..."}
// JSON objects that some models emit into inline CMD:/READ: markers.
func unwrapJSONActionCommands(response string) string {
	var out []string
	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "{") {
			out = append(out, line)
			continue
		}
		converted := convertJSONActionCommand(trimmed)
		if converted != "" {
			out = append(out, converted)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func convertJSONActionCommand(line string) string {
	var obj struct {
		Action  string `json:"action"`
		Command string `json:"command"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal([]byte(line), &obj); err != nil {
		return ""
	}
	switch strings.ToUpper(obj.Action) {
	case "CMD", "COMMAND":
		if cmd := strings.TrimSpace(obj.Command); cmd != "" {
			lower := strings.ToLower(cmd)
			if strings.HasPrefix(lower, "read:") || strings.HasPrefix(lower, "edit:") ||
				strings.HasPrefix(lower, "write:") || strings.HasPrefix(lower, "cmd:") {
				return cmd
			}
			return "CMD: " + cmd
		}
	case "READ":
		if path := strings.TrimSpace(obj.Path); path != "" {
			return "READ: " + path
		}
	case "EDIT":
		if path := strings.TrimSpace(obj.Path); path != "" {
			return "EDIT: " + path
		}
	case "WRITE":
		if path := strings.TrimSpace(obj.Path); path != "" {
			return "WRITE: " + path
		}
	}
	return ""
}

// unwrapJSONToolCallCommand converts {"toolCall":"toolCall","tool":"CMD","arguments":{"command":"..."}}
// JSON objects that some models emit as structured function call output.
func unwrapJSONToolCallCommand(response string) string {
	if !strings.Contains(response, `"toolCall"`) {
		return response
	}
	var obj struct {
		ToolCall  string `json:"toolCall"`
		Tool      string `json:"tool"`
		Arguments struct {
			Command string `json:"command"`
			Path    string `json:"path"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(response), &obj); err != nil {
		return response
	}
	if obj.ToolCall == "" || obj.Tool == "" {
		return response
	}
	switch strings.ToUpper(obj.Tool) {
	case "CMD":
		if cmd := strings.TrimSpace(obj.Arguments.Command); cmd != "" {
			return "CMD: " + cmd
		}
	case "READ":
		if path := strings.TrimSpace(obj.Arguments.Path); path != "" {
			return "READ: " + path
		}
	case "WRITE":
		if path := strings.TrimSpace(obj.Arguments.Path); path != "" {
			return "WRITE: " + path
		}
	}
	return response
}

// unwrapAngleBracketCMD converts `<CMD: command>`, `<CMD: command</CMD>`, and
// multi-line `<CMD>\ncommand\n</CMD>` to `CMD: command`.
// Some models emit XML-style tags around CMD lines instead of the plain CMD: prefix.
func unwrapAngleBracketCMD(response string) string {
	if !strings.Contains(response, "<CMD:") && !strings.Contains(response, "<cmd:") &&
		!strings.Contains(response, "<CMD>") && !strings.Contains(response, "<cmd>") {
		return response
	}
	var out []string
	inCMDBlock := false
	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		upper := strings.ToUpper(trimmed)
		if strings.HasPrefix(trimmed, "</CMD>") || strings.HasPrefix(trimmed, "</cmd>") {
			inCMDBlock = false
			continue
		}
		if strings.HasPrefix(upper, "<CMD:") {
			inner := strings.TrimSpace(trimmed[5:]) // after "<CMD:"
			// Strip trailing `>` (self-closing tag) or `</CMD>` (tag pair).
			if idx := strings.Index(inner, "</CMD>"); idx >= 0 {
				inner = strings.TrimSpace(inner[:idx])
			} else {
				inner = strings.TrimSuffix(inner, ">")
			}
			if inner != "" {
				out = append(out, "CMD: "+inner)
				continue
			}
		}
		if upper == "<CMD>" || upper == "<CMD />" {
			inCMDBlock = true
			continue
		}
		// Handle inline <CMD>text</CMD> on a single line.
		if strings.HasPrefix(upper, "<CMD>") && strings.Contains(upper, "</CMD>") {
			inner := strings.TrimSpace(trimmed[5:]) // after "<CMD>"
			if idx := strings.Index(strings.ToUpper(inner), "</CMD>"); idx >= 0 {
				inner = strings.TrimSpace(inner[:idx])
			}
			if inner != "" {
				out = append(out, "CMD: "+inner)
				continue
			}
		}
		if inCMDBlock {
			if trimmed != "" {
				out = append(out, "CMD: "+trimmed)
			}
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
// JSON arrays that some LLMs (e.g. Gemini) emit natively. Converts to inline CMD:/READ:/EDIT:/WRITE: markers.
func unwrapJSONCommandArray(response string) string {
	if !strings.Contains(response, `"commands"`) {
		return response
	}
	var obj struct {
		Commands []struct {
			Keystrokes string `json:"keystrokes"`
			Cmd        string `json:"cmd"`
		} `json:"commands"`
	}
	if err := json.Unmarshal([]byte(response), &obj); err != nil {
		return response
	}
	if len(obj.Commands) == 0 {
		return response
	}
	// Extract just the commands and append them as inline markers to the response
	var parts []string
	for _, c := range obj.Commands {
		s := strings.TrimSpace(c.Keystrokes)
		if s == "" {
			s = strings.TrimSpace(c.Cmd)
		}
		if s == "" {
			continue
		}
		lower := strings.ToLower(s)
		if strings.HasPrefix(lower, "read:") || strings.HasPrefix(lower, "edit:") ||
			strings.HasPrefix(lower, "write:") || strings.HasPrefix(lower, "cmd:") {
			parts = append(parts, s)
		} else {
			parts = append(parts, "CMD: "+s)
		}
	}
	if len(parts) == 0 {
		return response
	}
	return response + "\n" + strings.Join(parts, "\n")
}

// unwrapDSMLToolCalls converts DSML XML tool call blocks into CMD: lines.
// Some LLMs emit <DSML><invoke name="bash"><parameter name="command" string="true">CMD</parameter></invoke></DSML>
func unwrapDSMLToolCalls(response string) string {
	if !strings.Contains(response, "<DSML") {
		return response
	}
	var out []string
	for _, block := range splitDSMLBlocks(response) {
		cmd := extractDSMLCommand(block)
		if cmd != "" {
			out = append(out, cmd)
		}
	}
	if len(out) == 0 {
		return response
	}
	return response + "\n" + strings.Join(out, "\n")
}

// splitDSMLBlocks finds all DSML blocks in a string (both <DSML> and <｜DSML｜ formats).
func splitDSMLBlocks(s string) []string {
	var blocks []string
	for {
		// Try <｜DSML｜ first (DeepSeek v4 full-width bar format)
		start := strings.Index(s, "<\uFF5CDSML\uFF5C")
		endTag := "</\uFF5CDSML\uFF5C"
		if start < 0 {
			// Fall back to original <DSML format
			start = strings.Index(s, "<DSML")
			endTag = "</DSML>"
		}
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], endTag)
		if end < 0 {
			break
		}
		end += start + len(endTag)
		blocks = append(blocks, s[start:end])
		s = s[end:]
	}
	return blocks
}

// extractDSMLCommand extracts the bash command from a DSML block.
func extractDSMLCommand(block string) string {
	// Try to find name="command" or name="bash" parameter
	nameTag := `<parameter name="command" string="true">`
	i := strings.Index(block, nameTag)
	if i < 0 {
		// Try name="bash"
		nameTag = `<parameter name="bash" string="true">`
		i = strings.Index(block, nameTag)
	}
	if i < 0 {
		return ""
	}
	i += len(nameTag)
	end := strings.Index(block[i:], "</parameter>")
	if end < 0 {
		return ""
	}
	cmd := strings.TrimSpace(block[i : i+end])
	// Also try extract the invoke name to map to proper prefix
	lower := strings.ToLower(cmd)
	if strings.HasPrefix(lower, "read:") || strings.HasPrefix(lower, "edit:") ||
		strings.HasPrefix(lower, "write:") || strings.HasPrefix(lower, "cmd:") {
		return cmd
	}
	return "CMD: " + cmd
}

// normalizeNativeEditEndLines fixes common model typos (e.g. >>>>>> REPLACE → >>>>>>> REPLACE).
func normalizeNativeEditEndLines(response string) string {
	var out []string
	for _, line := range strings.Split(response, "\n") {
		if isShortNativeEditEndMarker(strings.TrimSpace(line)) {
			out = append(out, nativeEditEndMarker)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// unwrapFunctionToolCalls converts <Function><functionName>cmdi</functionName><functionArgs>{...}</functionArgs></Function>
// blocks into CMD: lines. Some LLMs emit this XML format for tool calls.
func unwrapFunctionToolCalls(response string) string {
	if !strings.Contains(response, "<Function>") {
		return response
	}
	var out []string
	for _, block := range splitFunctionBlocks(response) {
		cmd := extractFunctionCommand(block)
		if cmd != "" {
			out = append(out, cmd)
		}
	}
	if len(out) == 0 {
		return response
	}
	return response + "\n" + strings.Join(out, "\n")
}

// splitFunctionBlocks finds all <Function>...</Function> blocks in a string.
func splitFunctionBlocks(s string) []string {
	var blocks []string
	for {
		start := strings.Index(s, "<Function>")
		if start < 0 {
			break
		}
		end := strings.Index(s[start:], "</Function>")
		if end < 0 {
			break
		}
		end += start + 11 // len("</Function>")
		blocks = append(blocks, s[start:end])
		s = s[end:]
	}
	return blocks
}

// extractFunctionCommand extracts the command from a Function block.
func extractFunctionCommand(block string) string {
	// Extract functionName
	fnStart := strings.Index(block, "<functionName>")
	fnEnd := strings.Index(block, "</functionName>")
	if fnStart < 0 || fnEnd < 0 || fnEnd <= fnStart {
		return ""
	}
	fnName := strings.TrimSpace(block[fnStart+12 : fnEnd])

	// Extract functionArgs
	argsStart := strings.Index(block, "<functionArgs>")
	argsEnd := strings.Index(block, "</functionArgs>")
	if argsStart < 0 || argsEnd < 0 || argsEnd <= argsStart {
		return ""
	}
	argsJSON := strings.TrimSpace(block[argsStart+13 : argsEnd])

	// Only handle "cmdi" function for now
	if fnName != "cmdi" {
		return ""
	}

	// Parse JSON args to get cmd
	var args struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return ""
	}
	cmd := strings.TrimSpace(args.Cmd)
	if cmd == "" {
		return ""
	}
	return "CMD: " + cmd
}

var gluedWriteBodyRE = regexp.MustCompile(`(?i)^(WRITE:\s*\S+)\s+(package\s|import\s|from\s|#include\b|<\?php\b)`)

func normalizeGluedWriteBody(response string) string {
	var out []string
	for _, line := range strings.Split(response, "\n") {
		if m := gluedWriteBodyRE.FindStringSubmatch(line); len(m) >= 2 {
			out = append(out, m[1])
			rest := strings.TrimSpace(line[len(m[1]):])
			out = append(out, rest)
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// isShortNativeEditEndMarker reports >>>>>> REPLACE (six arrows) and similar typos.
func isShortNativeEditEndMarker(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.EqualFold(line, nativeEditEndMarker) || strings.EqualFold(line, "---END EDIT---") {
		return false
	}
	upper := strings.ToUpper(line)
	if !strings.HasSuffix(upper, "REPLACE") {
		return false
	}
	i := 0
	for i < len(upper) && upper[i] == '>' {
		i++
	}
	if i < 6 || i >= 7 {
		return false
	}
	rest := strings.TrimSpace(upper[i:])
	return rest == "REPLACE"
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
	var fenceLang string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !inFence {
			if strings.HasPrefix(trimmed, "```") {
				inner := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
				upper := strings.ToUpper(inner)
			if strings.HasPrefix(upper, "CMD:") || strings.HasPrefix(upper, "READ:") ||
				strings.HasPrefix(upper, "EDIT:") || strings.HasPrefix(upper, "WRITE:") {
				parts := strings.SplitN(inner, " ", 2)
				if len(parts) == 2 {
					out = append(out, strings.ToUpper(parts[0])+" "+parts[1])
				} else {
					out = append(out, strings.ToUpper(inner))
				}
				continue
			}
				if isMarkdownToolFenceLang(inner) || upper == "CMD" {
					inFence = true
					fenceLang = upper
					continue
				}
			}
			out = append(out, line)
			continue
		}
		if trimmed == "```" {
			inFence = false
			fenceLang = ""
			continue
		}
		if fenceLang == "CMD" {
			out = append(out, "CMD: "+line)
		} else {
			out = append(out, line)
		}
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
			continue
		}
		if extra := formatDoubleEqualsEditFeedback(lines, i); extra != "" {
			msgs = append(msgs, extra)
		}
	}
	if strings.Contains(strings.ToUpper(response), "---END EDIT---") {
		msgs = append(msgs, "Do not use ---END EDIT--- — end EDIT blocks with a line containing only "+nativeEditEndMarker)
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

// unwrapMarkdownBoldToolLines strips `**` from `**CMD: ...**` or `**CMD:` (common model mistake).
func unwrapMarkdownBoldToolLines(response string) string {
	var out []string
	for _, line := range strings.Split(response, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "**") {
			inner := strings.TrimPrefix(trimmed, "**")
			inner = strings.TrimSuffix(inner, "**")
			inner = strings.TrimSpace(inner)
			upper := strings.ToUpper(inner)
			if strings.HasPrefix(upper, "CMD:") || strings.HasPrefix(upper, "READ:") ||
				strings.HasPrefix(upper, "EDIT:") || strings.HasPrefix(upper, "WRITE:") {
				out = append(out, normalizeOrchestratedToolLabel(inner))
				continue
			}
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

// scrubNativeEditLinesFromShellCommand drops lines like ---END EDIT--- the model glues after CMD blocks.
func scrubNativeEditLinesFromShellCommand(cmd string) (string, bool) {
	var kept []string
	changed := false
	for _, line := range strings.Split(cmd, "\n") {
		if isOrchestratedShellNoiseLine(line) {
			changed = true
			continue
		}
		kept = append(kept, line)
	}
	out := strings.TrimSpace(strings.Join(kept, "\n"))
	if out == "" {
		return cmd, false
	}
	return out, changed
}

// normalizeOrchestratedToolLabel fixes `CMD:** export` (markdown bold glued to colon).
func normalizeOrchestratedToolLabel(line string) string {
	line = strings.TrimSpace(line)
	upper := strings.ToUpper(line)
	for _, prefix := range []string{"CMD:", "READ:", "EDIT:", "WRITE:"} {
		if !strings.HasPrefix(upper, prefix) {
			continue
		}
		rest := strings.TrimSpace(line[len(prefix):])
		rest = strings.TrimPrefix(rest, ":")
		rest = strings.TrimPrefix(rest, "**")
		rest = strings.TrimSpace(rest)
		return prefix + " " + rest
	}
	return line
}

// stripLeadingMarkdownBoldFromShell removes leading `**` the model pastes before shell commands.
func stripLeadingMarkdownBoldFromShell(cmd string) string {
	trimmed := strings.TrimSpace(cmd)
	for strings.HasPrefix(trimmed, "**") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "**"))
	}
	if strings.HasPrefix(strings.ToUpper(trimmed), "CMD:") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(trimmed[4:]), ":"))
	}
	return trimmed
}

// sanitizeOrchestratedShellCommand trims model prose/JSON glued onto shell commands.
func sanitizeOrchestratedShellCommand(cmd string) (string, bool) {
	changed := false
	if stripped := stripLeadingMarkdownBoldFromShell(cmd); stripped != cmd {
		cmd = stripped
		changed = true
	}
	if scrubbed, ok := scrubNativeEditLinesFromShellCommand(cmd); ok {
		cmd = scrubbed
		changed = true
	}
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
	// Strip trailing ** from model markdown-bold leakage (e.g. "pytest**").
	cmd = trailingStarsRE.ReplaceAllString(cmd, "")
	return strings.TrimSpace(cmd), changed
}

var trailingStarsRE = regexp.MustCompile(`\*{1,3}\s*$`)

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
		if rest == "{}" || rest == "{ }" {
			return strings.TrimSpace(cmd[:i]), true
		}
	}
	return cmd, false
}

// proseAfterExtRE matches when a file extension is immediately followed by an
// English sentence-starting word with no delimiter (e.g. "SPEC.mdThe arch...").
// This catches the common model mistake of gluing reasoning prose after a CMD.
var proseAfterExtRE = regexp.MustCompile(`(?i)(\.[a-z]{2,4})(The\s+(?:architecture|document|tool|project|above|following|next|previous|current)|We\s+(?:need|can|will|have|are|should|must)|This\s+(?:is|was|has)|It\s+(?:is|was|has|seems)|Let's\s+(?:try|see|test)|So\s+(?:the|let's)|In\s+(?:earlier|summary|order))`)

// stripTrailingProse removes English prose glued to the end of a shell command
// (common model mistake when the LLM continues reasoning on the same line).
func stripTrailingProse(cmd string) string {
	if m := proseAfterExtRE.FindStringSubmatch(cmd); len(m) >= 3 {
		ext := m[1]   // .md, .go, etc.
		prose := m[2] // "The architecture...", "We need...", etc.
		// Find the extension in the command and truncate at that point.
		if idx := strings.Index(cmd, ext+prose); idx >= 0 {
			return strings.TrimSpace(cmd[:idx+len(ext)])
		}
	}
	return cmd
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
	if cleaned := stripCommandOutputArtifacts(cmd); cleaned != cmd {
		cmd = cleaned
		changed = true
		lower = strings.ToLower(cmd)
	}
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

// stripCommandOutputArtifacts removes pasted tool output from shell commands (e.g. "(no output)").
func stripCommandOutputArtifacts(cmd string) string {
	for _, junk := range []string{"(exit 0, no output)", "(no output)"} {
		cmd = strings.ReplaceAll(cmd, junk, "")
	}
	cmd = bdListStatusGluedRE.ReplaceAllString(cmd, "--status=$1")
	return strings.TrimSpace(cmd)
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
	if t == nativeEditEndMarker || strings.EqualFold(t, "---END EDIT---") {
		return true
	}
	return isShortNativeEditEndMarker(t)
}

// formatDoubleEqualsEditFeedback detects git-merge style EDIT bodies (two ======= dividers).
func formatDoubleEqualsEditFeedback(lines []string, editLine int) string {
	equals := 0
	sawSearch := false
	for j := editLine + 1; j < len(lines) && j < editLine+80; j++ {
		t := strings.TrimSpace(lines[j])
		tu := strings.ToUpper(t)
		if strings.HasPrefix(tu, "EDIT:") || strings.HasPrefix(tu, "WRITE:") ||
			strings.HasPrefix(tu, "CMD:") {
			break
		}
		if t == nativeEditSearchMarker || strings.HasPrefix(t, "<<<<<<<") {
			sawSearch = true
			continue
		}
		if sawSearch && t == nativeEditReplaceMarker {
			equals++
		}
		if isNativeEditEndMarker(t) {
			break
		}
	}
	if equals < 2 {
		return ""
	}
	return "EDIT: uses two " + nativeEditReplaceMarker + " lines (git-merge style) — use exactly one SEARCH block, one " + nativeEditReplaceMarker + ", then >>>>>>> REPLACE"
}

// isOrchestratedShellNoiseLine reports a single-line token that must never run as shell
// (native edit terminators the model emits without CMD:).
func isOrchestratedShellNoiseLine(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	if isNativeEditEndMarker(t) || isOrchestratedNativeToolLine(t) {
		return true
	}
	upper := strings.ToUpper(t)
	return strings.HasPrefix(upper, "<<<<<<<") || strings.HasPrefix(upper, ">>>>>>>") || t == "======="
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
	case "EOF", "EOT", "END", "---":
		return true
	}
	t := strings.TrimSpace(line)
	return strings.EqualFold(t, nativeEditWriteEnd) ||
		strings.EqualFold(t, "---END EDIT---") ||
		strings.EqualFold(t, "---END WRITE---")
}

// ToolCall represents an extracted tool invocation from any format.
type ToolCall struct {
	Tool    string
	Content string
}

var ff = "\uFF5C"

// mapToolName maps various LLM tool names to canonical codes.
func mapToolName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "bash", "cmd", "command", "cmdi", "shell":
		return "CMD"
	case "read", "read_file":
		return "READ"
	case "write", "write_file", "wf":
		return "WRITE"
	case "edit", "edit_file", "ef":
		return "EDIT"
	default:
		return ""
	}
}

// parseOrchestrated tries all sub-parsers in priority order and combines unique results.
func parseOrchestrated(s string) []ToolCall {
	var result []ToolCall
	seen := make(map[string]bool)
	add := func(tcs []ToolCall) {
		for _, tc := range tcs {
			key := tc.Tool + ":" + tc.Content
			if !seen[key] {
				seen[key] = true
				result = append(result, tc)
			}
		}
	}
	add(parseDSML(s))
	add(parseFunctionXML(s))
	add(parseCmdTag(s))
	add(parseJSON(s))
	add(parseMarkdownFence(s))
	return result
}

func findToolName(block string) string {
	// Try <invoke name="X"> or <｜DSML｜invoke name="X"
	prefixes := []string{`<invoke name="`, `<` + ff + `DSML` + ff + `invoke name="`}
	for _, p := range prefixes {
		i := strings.Index(block, p)
		if i >= 0 {
			start := i + len(p)
			end := strings.IndexByte(block[start:], '"')
			if end >= 0 {
				return block[start : start+end]
			}
		}
	}
	return ""
}

func extractDSMLParams(block string) map[string]string {
	params := make(map[string]string)
	// Standard <parameter name="X" ...>content</parameter>
	paramPattern := `<parameter name="`
	rest := block
	for {
		i := strings.Index(rest, paramPattern)
		if i < 0 {
			break
		}
		rest = rest[i+len(paramPattern):]
		j := strings.IndexByte(rest, '"')
		if j < 0 {
			break
		}
		name := rest[:j]
		rest = rest[j+1:]
		gt := strings.IndexByte(rest, '>')
		if gt < 0 {
			break
		}
		content := rest[gt+1:]
		closeTag := "</parameter>"
		k := strings.Index(content, closeTag)
		if k >= 0 {
			params[name] = content[:k]
			rest = content[k+len(closeTag):]
		}
	}
	// DSML style: ｜DSML｜parameter name="X" ...>content</｜DSML｜parameter>
	dsmlPattern := ff + `DSML` + ff + `parameter name="`
	rest = block
	for {
		i := strings.Index(rest, dsmlPattern)
		if i < 0 {
			break
		}
		rest = rest[i+len(dsmlPattern):]
		j := strings.IndexByte(rest, '"')
		if j < 0 {
			break
		}
		name := rest[:j]
		rest = rest[j+1:]
		gt := strings.IndexByte(rest, '>')
		if gt < 0 {
			break
		}
		content := rest[gt+1:]
		closeTag := "</" + ff + `DSML` + ff + `parameter>`
		k := strings.Index(content, closeTag)
		if k >= 0 {
			params[name] = content[:k]
			rest = content[k+len(closeTag):]
		}
	}
	return params
}

func extractDSMLParamByTool(params map[string]string, tool string) string {
	var keys []string
	switch tool {
	case "CMD":
		keys = []string{"command", "cmd", "bash"}
	case "READ", "WRITE", "EDIT":
		keys = []string{"file_path", "path", "fp"}
	}
	for _, k := range keys {
		if v, ok := params[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

func extractStandardDSMLBlock(block string) ToolCall {
	toolName := findToolName(block)
	if toolName == "" {
		return ToolCall{}
	}
	tool := mapToolName(toolName)
	if tool == "" {
		return ToolCall{}
	}
	params := extractDSMLParams(block)
	content := extractDSMLParamByTool(params, tool)
	if content == "" {
		return ToolCall{}
	}
	return ToolCall{Tool: tool, Content: content}
}

func extractDeepSeekDSMLBlock(block string) []ToolCall {
	var result []ToolCall
	lines := strings.Split(block, "\n")
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		invokePrefix := "<" + ff + `DSML` + ff + `invoke name="`
		if !strings.HasPrefix(trimmed, invokePrefix) {
			continue
		}
		rest := trimmed[len(invokePrefix):]
		end := strings.IndexByte(rest, '"')
		if end < 0 {
			continue
		}
		toolName := rest[:end]
		tool := mapToolName(toolName)
		if tool == "" {
			continue
		}
		// Collect all params for this invoke
		params := make(map[string]string)
		i++
		for i < len(lines) {
			inner := strings.TrimSpace(lines[i])
			// Check for next invoke or end of wrapper
			if strings.HasPrefix(inner, invokePrefix) ||
				strings.HasPrefix(inner, "</"+ff+"DSML"+ff+"tool_calls>") {
				i--
				break
			}
			// Check for close invoke marker
			if strings.HasPrefix(inner, "/"+ff+"DSML"+ff+"invoke") ||
				strings.HasPrefix(inner, "</"+ff+"DSML"+ff+"invoke>") {
				break
			}
			// Extract parameter content
			lineParams := extractDSMLParams(lines[i])
			for k, v := range lineParams {
				params[k] = v
			}
			i++
		}
		content := extractDSMLParamByTool(params, tool)
		if content != "" {
			result = append(result, ToolCall{Tool: tool, Content: content})
		}
	}
	return result
}

func parseDSML(s string) []ToolCall {
	var result []ToolCall
	remain := s
	for {
		// Try DeepSeek v4 format first: <｜DSML｜tool_calls>...</｜DSML｜tool_calls>
		dsmlWrapper := "<" + ff + `DSML` + ff + `tool_calls>`
		dsmlEnd := "</" + ff + `DSML` + ff + `tool_calls>`
		start := strings.Index(remain, dsmlWrapper)
		endTag := dsmlEnd
		if start < 0 {
			// Try standard: <DSML>...</DSML>
			start = strings.Index(remain, "<DSML")
			endTag = "</DSML>"
		}
		if start < 0 {
			break
		}
		end := strings.Index(remain[start:], endTag)
		if end < 0 {
			break
		}
		end += start + len(endTag)
		block := remain[start:end]
		if endTag == dsmlEnd {
			result = append(result, extractDeepSeekDSMLBlock(block)...)
		} else {
			if tc := extractStandardDSMLBlock(block); tc.Tool != "" {
				result = append(result, tc)
			}
		}
		remain = remain[end:]
	}
	return result
}

func parseFunctionXML(s string) []ToolCall {
	var result []ToolCall
	remain := s
	for {
		start := strings.Index(remain, "<invoke")
		if start < 0 {
			break
		}
		end := strings.Index(remain[start:], "</invoke>")
		if end < 0 {
			break
		}
		end += start + len("</invoke>")
		block := remain[start:end]
		tc := extractStandardDSMLBlock(block)
		if tc.Tool != "" {
			result = append(result, tc)
		}
		remain = remain[end:]
	}
	return result
}

func parseCmdTag(s string) []ToolCall {
	var result []ToolCall
	remain := s
	for {
		start := strings.Index(remain, "<cmd>")
		if start < 0 {
			start = strings.Index(remain, "<CMD>")
		}
		if start < 0 {
			break
		}
		end := strings.Index(remain[start:], "</cmd>")
		if end < 0 {
			end = strings.Index(remain[start:], "</CMD>")
		}
		if end < 0 {
			break
		}
		end += start
		content := strings.TrimSpace(remain[start+5 : end])
		if content == "" {
			remain = remain[end:]
			continue
		}
		result = append(result, ToolCall{Tool: "CMD", Content: content})
		remain = remain[end:]
	}
	return result
}

func parseJSON(s string) []ToolCall {
	clean := s
	// Strip markdown code fence if present
	if strings.HasPrefix(clean, "```") || strings.HasPrefix(clean, "````") {
		lines := strings.Split(clean, "\n")
		first := strings.TrimSpace(lines[0])
		lang := strings.TrimSpace(first[3:])
		if strings.ToLower(lang) == "json" || lang == "" {
			// Find closing fence
			for i := len(lines) - 1; i > 0; i-- {
				if strings.TrimSpace(lines[i]) == "```" {
					clean = strings.TrimSpace(strings.Join(lines[1:i], "\n"))
					break
				}
			}
		}
	}

	// Try tool_calls format first
	var toolCallsObj struct {
		ToolCalls []struct {
			Name string                 `json:"name"`
			Args map[string]interface{} `json:"args"`
		} `json:"tool_calls"`
	}

	if err := json.Unmarshal([]byte(clean), &toolCallsObj); err == nil && len(toolCallsObj.ToolCalls) > 0 {
		var result []ToolCall
		for _, tc := range toolCallsObj.ToolCalls {
			tool := mapToolName(tc.Name)
			if tool == "" {
				continue
			}
			content := ""
			keys := []string{"cmd", "command", "file_path", "path", "fp"}
			for _, key := range keys {
				if v, ok := tc.Args[key]; ok {
					if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
						content = strings.TrimSpace(s)
						break
					}
				}
			}
			if content == "" {
				continue
			}
			result = append(result, ToolCall{Tool: tool, Content: content})
		}
		return result
	}

	// Try function format: {"function":"X","args":"..."} or {"function":"X","args":{...}}
	var funcObj struct {
		Function string          `json:"function"`
		Args     json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal([]byte(clean), &funcObj); err == nil && funcObj.Function != "" {
		tool := mapToolName(funcObj.Function)
		if tool != "" {
			content := ""
			// Try args as object
			var argsObj map[string]interface{}
			if err := json.Unmarshal(funcObj.Args, &argsObj); err == nil {
				keys := []string{"cmd", "command", "file_path", "path", "fp"}
				for _, key := range keys {
					if v, ok := argsObj[key]; ok {
						if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
							content = strings.TrimSpace(s)
							break
						}
					}
				}
			} else {
				// Try args as string (JSON string)
				var argsStr string
				if err := json.Unmarshal(funcObj.Args, &argsStr); err == nil {
					var inner map[string]interface{}
					if err := json.Unmarshal([]byte(argsStr), &inner); err == nil {
						keys := []string{"cmd", "command", "file_path", "path", "fp"}
						for _, key := range keys {
							if v, ok := inner[key]; ok {
								if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
									content = strings.TrimSpace(s)
									break
								}
							}
						}
					}
				}
			}
			if content != "" {
				return []ToolCall{{Tool: tool, Content: content}}
			}
		}
	}

	return nil
}

func parseMarkdownFence(s string) []ToolCall {
	var result []ToolCall
	lines := strings.Split(s, "\n")
	i := 0
	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, "```") {
			lang := strings.TrimSpace(trimmed[3:])
			// Determine if this language indicates a tool command
			isShellLang := false
			switch strings.ToLower(lang) {
			case "", "bash", "sh", "shell":
				isShellLang = true
			}
			// Find closing fence
			j := i + 1
			for j < len(lines) {
				if strings.TrimSpace(lines[j]) == "```" {
					break
				}
				j++
			}
			if j < len(lines) {
				content := strings.TrimSpace(strings.Join(lines[i+1:j], "\n"))
				if isShellLang && content != "" {
					result = append(result, ToolCall{Tool: "CMD", Content: content})
				}
				i = j + 1
				continue
			}
		}
		i++
	}
	return result
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
