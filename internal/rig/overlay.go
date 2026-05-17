package rig

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/style"
)

func gasTownIgnorePatterns() []string {
	return []string{
		".runtime/",
		".claude/",
		".logs/",
		"__pycache__/",
		".venv/",
		"venv/",
		"state.json",
		"CLAUDE.md",
		"CLAUDE.local.md",

		// Fix #95 (agent-internal plumbing leaking into rig commits):
		// The patterns below were observed COMMITTED into polecat work
		// branches and the rig clone's main, polluting every subsequent
		// polecat worktree with stale agent identity, scratch files,
		// and terminal recordings. Symptoms included Fix #93 (polecat
		// starting with role: refinery because `.gt-agent` was tracked
		// in main with refinery's identity) and 6000+ insertion
		// implementation commits where 99% of the diff was internal
		// gt-agent state, not user code. None of these files belong in
		// a project repo:
		//
		//   .gt-agent      — agent identity (role/rig/name JSON)
		//   typescript     — output of `script(1)` terminal recorder
		//   AGENTS.md      — agent role instructions
		//   SPEC.md        — agent-generated work spec
		//   project_label  — agent scratch tag
		//   progress_report — agent scratch progress log
		//   gt-agent-state.json — gt-agent persistent state across cycles
		//
		// SPEC.md and README.md are deliberately NOT in this list:
		// they are user-authored content (the project contract /
		// human-facing docs) and the polecat needs to READ them from
		// its worktree at runtime. The initial Fix #95 wrongly
		// included SPEC.md, which caused mockrig's project spec to
		// be silently ignored after the rig reset. AGENTS.md /
		// CLAUDE.md stay in: those are agent-internal prompt files
		// (overridable with `git add -f` if a user really wants
		// them tracked).
		".gt-agent",
		"typescript",
		"AGENTS.md",
		"project_label",
		"progress_report",
		"gt-agent-state.json",
	}
}

// CopyOverlay copies files from <rigPath>/.runtime/overlay/ to the destination path.
// This allows storing gitignored files (like .env) that services need at their root.
// The overlay is copied non-recursively - only files, not subdirectories.
// File permissions from the source are preserved.
//
// Structure:
//
//	rig/
//	  .runtime/
//	    overlay/
//	      .env          <- Copied to destPath
//	      config.json   <- Copied to destPath
//
// Returns nil if the overlay directory doesn't exist (nothing to copy).
// Individual file copy failures are logged as warnings but don't stop the process.
func CopyOverlay(rigPath, destPath string) error {
	overlayDir := filepath.Join(rigPath, ".runtime", "overlay")

	// Check if overlay directory exists
	entries, err := os.ReadDir(overlayDir)
	if err != nil {
		if os.IsNotExist(err) {
			// No overlay directory - not an error, just nothing to copy
			return nil
		}
		return fmt.Errorf("reading overlay dir: %w", err)
	}

	// Copy each file (not directories) from overlay to destination
	for _, entry := range entries {
		if entry.IsDir() {
			// Skip subdirectories - only copy files at overlay root
			continue
		}

		srcPath := filepath.Join(overlayDir, entry.Name())
		dstPath := filepath.Join(destPath, entry.Name())

		if err := copyFilePreserveMode(srcPath, dstPath); err != nil {
			// Log warning but continue - don't fail spawn for overlay issues
			style.PrintWarning("could not copy overlay file %s: %v", entry.Name(), err)
			continue
		}
	}

	return nil
}

// EnsureGitignorePatterns ensures the .gitignore has required Gas Town patterns.
// This is called after cloning to add patterns that may be missing from the source repo.
func EnsureGitignorePatterns(worktreePath string) error {
	gitignorePath := filepath.Join(worktreePath, ".gitignore")

	// Required patterns for Gas Town worktrees.
	// DO NOT add ".beads/" here. Beads manages its own .beads/.gitignore
	// (created by bd init) which selectively ignores runtime files.
	// Adding .beads/ here overrides that and breaks bd sync.
	// This has regressed twice (PR #753 added it, #891 removed it,
	// #966 re-added it). See overlay_test.go for a regression guard.
	//
	// .claude/ is the broad pattern (covers commands/, settings.json, rules/, etc.).
	// Settings are installed in gastown-managed parent directories via --settings flag,
	// but Cursor still creates .claude/ inside worktrees at runtime. The narrow
	// .claude/commands/ pattern missed other Cursor-created files, causing gt done
	// to fail with "uncommitted changes would be lost" on untracked .claude/ entries.
	requiredPatterns := gasTownIgnorePatterns()

	// Read existing gitignore content
	var existingContent string
	if data, err := os.ReadFile(gitignorePath); err == nil {
		existingContent = string(data)
	}

	// Find missing patterns
	var missing []string
	for _, pattern := range requiredPatterns {
		found := false
		for _, line := range strings.Split(existingContent, "\n") {
			line = strings.TrimSpace(line)
			if matchesGitignorePattern(line, pattern) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, pattern)
		}
	}

	if len(missing) == 0 {
		return nil // All patterns present
	}

	// Append missing patterns
	f, err := os.OpenFile(gitignorePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening .gitignore: %w", err)
	}
	defer f.Close()

	// Add header if appending to existing file
	if existingContent != "" && !strings.HasSuffix(existingContent, "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	if existingContent != "" {
		if _, err := f.WriteString("\n# Gas Town (added by gt)\n"); err != nil {
			return err
		}
	}

	for _, pattern := range missing {
		if _, err := f.WriteString(pattern + "\n"); err != nil {
			return err
		}
	}

	return nil
}

// gasTownLocalExcludePatterns returns the patterns to write to the worktree-local
// .git/info/exclude file. This is a superset of gasTownIgnorePatterns() and
// includes .beads/ — which is safe here because .git/info/exclude is per-worktree
// and never committed to the repo (unlike .gitignore, where .beads/ must NOT appear
// because Beads manages its own .beads/.gitignore via bd init).
func gasTownLocalExcludePatterns() []string {
	patterns := gasTownIgnorePatterns()
	// .beads/ is excluded from gasTownIgnorePatterns() to avoid breaking bd sync
	// (see EnsureGitignorePatterns comment). The local exclude file is safe to
	// include it — it's per-worktree and invisible to `git status` without affecting
	// the tracked .gitignore (gas-7vg defense-in-depth).
	return append(patterns, ".beads/")
}

// agentInternalUntrackPaths is the subset of gasTownIgnorePatterns that are
// expected to NEVER appear in user repos. If we find them already tracked
// in a polecat worktree (because they were committed to the rig long ago
// and the polecat's branch inherits them), we `git rm --cached` them so
// `git add -A` stops re-committing modifications. See Fix #95b.
//
// We restrict to single-file patterns here (no globs / directories) because
// `git rm --cached <pattern>` only takes literal paths and we don't want
// to recursively untrack a user's content by accident. Directory ignores
// like `.runtime/` are handled by the existing exclude / .gitignore path.
var agentInternalUntrackPaths = []string{
	".gt-agent",
	"typescript",
	"AGENTS.md",
	"SPEC.md",
	"project_label",
	"progress_report",
	"gt-agent-state.json",
	"CLAUDE.md",
	"CLAUDE.local.md",
}

// untrackAgentInternals removes agent-internal plumbing files from the git
// INDEX (without deleting them from the working tree) for any path in
// agentInternalUntrackPaths that is currently tracked in `worktreePath`.
//
// Fix #95b (already-tracked plumbing). Fix #95 adds patterns to
// `.git/info/exclude` so NEW agent files (`.gt-agent`, `typescript`, ...)
// stay out of commits. But once a file is tracked, `.gitignore` /
// exclude are silently overridden — `git add -A` keeps committing changes.
// We saw exactly this in the E2E #7 run: the polecat's "Implementation
// complete" commit was 2793 lines of `typescript` (terminal recorder
// output) and zero user files, because `typescript` was tracked from a
// prior polecat branch and `git add -A` swept its modifications in.
//
// Untracking here is safe:
//   - We use `--cached` so the file stays on disk; nothing is destroyed.
//   - These paths are agent-internal by definition (see Fix #95 commit
//     for the full rationale on each filename).
//   - This runs on every polecat spawn / refinery clone, so any new file
//     introduction (via a future feature) is automatically cleaned next
//     pass.
//
// Errors are non-fatal — if `git rm --cached` fails on a path we log a
// warning and move on rather than aborting the spawn.
func untrackAgentInternals(worktreePath string) {
	for _, path := range agentInternalUntrackPaths {
		// Skip paths not currently tracked. `git ls-files --error-unmatch`
		// exits non-zero when the path isn't tracked, which we treat as
		// "nothing to do" — the common case.
		check := exec.Command("git", "-C", worktreePath, "ls-files", "--error-unmatch", path)
		if err := check.Run(); err != nil {
			continue
		}
		rm := exec.Command("git", "-C", worktreePath, "rm", "--cached", "--quiet", "--", path)
		if out, err := rm.CombinedOutput(); err != nil {
			style.PrintWarning("could not untrack %s in %s: %v: %s",
				path, worktreePath, err, strings.TrimSpace(string(out)))
		}
	}
}

// EnsureLocalExcludePatterns writes the standard Gas Town ignore patterns to the
// worktree-local git exclude file so the worktree stays clean without mutating a
// tracked .gitignore.
func EnsureLocalExcludePatterns(worktreePath string) error {
	// Fix #95b: untrack any agent-internal files that snuck into the
	// index via previous polecat branches. Must happen BEFORE the
	// exclude rules are added, otherwise the polecat's first
	// `git add -A` will still re-commit them.
	untrackAgentInternals(worktreePath)

	excludePath, err := gitLocalExcludePath(worktreePath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(excludePath), 0755); err != nil {
		return fmt.Errorf("creating local exclude dir: %w", err)
	}

	var existingContent string
	if data, err := os.ReadFile(excludePath); err == nil {
		existingContent = string(data)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("reading local exclude: %w", err)
	}

	var missing []string
	for _, pattern := range gasTownLocalExcludePatterns() {
		found := false
		for _, line := range strings.Split(existingContent, "\n") {
			line = strings.TrimSpace(line)
			if matchesGitignorePattern(line, pattern) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, pattern)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("opening local exclude: %w", err)
	}
	defer f.Close()

	if existingContent != "" && !strings.HasSuffix(existingContent, "\n") {
		if _, err := f.WriteString("\n"); err != nil {
			return err
		}
	}
	if existingContent != "" {
		if _, err := f.WriteString("\n# Gas Town (added by gt)\n"); err != nil {
			return err
		}
	}

	for _, pattern := range missing {
		if _, err := f.WriteString(pattern + "\n"); err != nil {
			return err
		}
	}

	return nil
}

func gitLocalExcludePath(worktreePath string) (string, error) {
	cmd := exec.Command("git", "-C", worktreePath, "rev-parse", "--git-dir")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolving git dir: %w: %s", err, strings.TrimSpace(string(out)))
	}

	gitDir := strings.TrimSpace(string(out))
	if gitDir == "" {
		return "", fmt.Errorf("empty git dir for %s", worktreePath)
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(worktreePath, gitDir)
	}
	return filepath.Join(gitDir, "info", "exclude"), nil
}

// matchesGitignorePattern checks if a gitignore line covers the required pattern.
// Handles variant forms (with/without trailing slash, leading slash) and recognizes
// that a broader directory pattern (e.g., ".claude/") covers more specific paths
// (e.g., ".claude/commands/").
func matchesGitignorePattern(line, pattern string) bool {
	// Strip leading slash for comparison
	normLine := strings.TrimPrefix(line, "/")
	normPattern := strings.TrimPrefix(pattern, "/")

	// Exact match or trailing-slash variants
	if normLine == normPattern ||
		normLine == strings.TrimSuffix(normPattern, "/") ||
		normLine+"/" == normPattern {
		return true
	}

	// A broader directory pattern covers more specific paths underneath it.
	// e.g., ".claude/" covers ".claude/commands/"
	if strings.HasSuffix(normLine, "/") && strings.HasPrefix(normPattern, normLine) {
		return true
	}
	// Also handle directory pattern without trailing slash
	if !strings.Contains(normLine, "/") && strings.HasPrefix(normPattern, normLine+"/") {
		return true
	}

	return false
}

// copyFilePreserveMode copies a file from src to dst, preserving the source file's permissions.
func copyFilePreserveMode(src, dst string) error {
	// Get source file info for permissions
	srcInfo, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	// Open source file
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer srcFile.Close()

	// Create destination file with same permissions
	dstFile, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	// Copy contents
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		dstFile.Close()
		return fmt.Errorf("copy contents: %w", err)
	}

	// Explicitly check Close() — on many filesystems, buffered data is flushed
	// at Close() time, so a full-disk error surfaces here, not during Write.
	if err := dstFile.Close(); err != nil {
		return fmt.Errorf("closing destination: %w", err)
	}

	return nil
}
