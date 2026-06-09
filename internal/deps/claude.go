package deps

import (
	"context"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/steveyegge/gastown/internal/util"
)

// MinClaudeCodeVersion is the minimum compatible Claude Code version for Gas Town.
// v2.0.20 introduced the Skills system, which Gas Town uses for crew-commit,
// ghi-list, pr-list, and pr-sheriff. See docs/design/claude-code-minimum-version.md.
const MinClaudeCodeVersion = "2.0.20"

// RecommendedClaudeCodeVersion is the recommended version where skills and
// slash commands were merged into a unified system.
const RecommendedClaudeCodeVersion = "2.1.3"

// ClaudeCodeInstallURL is the installation page for Claude Code.
const ClaudeCodeInstallURL = "https://claude.ai/claude-code"

// ClaudeCodeStatus represents the state of the Claude Code installation.
type ClaudeCodeStatus int

const (
	ClaudeCodeOK         ClaudeCodeStatus = iota // claude found, version compatible
	ClaudeCodeNotFound                           // claude not in PATH
	ClaudeCodeTooOld                             // claude found but version too old
	ClaudeCodeOldButOK                           // claude found, above minimum but below recommended
	ClaudeCodeExecFailed                         // claude found but 'claude --version' failed
	ClaudeCodeUnknown                            // claude version ran but output couldn't be parsed
)

var (
	claudeCodeCheckOnce    sync.Once
	claudeCodeCheckStatus  ClaudeCodeStatus
	claudeCodeCheckVersion string
)

// CheckClaudeCode checks if Claude Code is installed and compatible.
// Returns status and the installed version (if found).
// The result is cached for the lifetime of the process: doctor and gt-agent may
// invoke this many times (including in parallel fix re-runs); claude --version
// is slow and can spawn Claude Code startup side effects.
func CheckClaudeCode() (ClaudeCodeStatus, string) {
	claudeCodeCheckOnce.Do(func() {
		claudeCodeCheckStatus, claudeCodeCheckVersion = checkClaudeCodeUncached()
	})
	return claudeCodeCheckStatus, claudeCodeCheckVersion
}

// ResetClaudeCodeCheckCache clears the CheckClaudeCode cache (for tests).
func ResetClaudeCodeCheckCache() {
	claudeCodeCheckOnce = sync.Once{}
	claudeCodeCheckStatus = 0
	claudeCodeCheckVersion = ""
}

func checkClaudeCodeUncached() (ClaudeCodeStatus, string) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return ClaudeCodeNotFound, ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	util.SetDetachedProcessGroup(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ClaudeCodeExecFailed, ""
	}

	version := parseClaudeCodeVersion(string(output))
	if version == "" {
		return ClaudeCodeUnknown, ""
	}

	if CompareVersions(version, MinClaudeCodeVersion) < 0 {
		return ClaudeCodeTooOld, version
	}

	if CompareVersions(version, RecommendedClaudeCodeVersion) < 0 {
		return ClaudeCodeOldButOK, version
	}

	return ClaudeCodeOK, version
}

// parseClaudeCodeVersion extracts version from Claude Code --version output.
// Output format: "2.1.101 (Claude Code)" or just "2.1.101".
func parseClaudeCodeVersion(output string) string {
	output = strings.TrimSpace(output)
	re := regexp.MustCompile(`^(\d+\.\d+\.\d+)`)
	matches := re.FindStringSubmatch(output)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}
