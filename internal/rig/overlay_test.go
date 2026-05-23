package rig

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyOverlay_NoOverlayDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	destDir := t.TempDir()

	// No overlay directory exists
	err := CopyOverlay(tmpDir, destDir)
	if err != nil {
		t.Errorf("CopyOverlay() with no overlay directory should return nil, got %v", err)
	}
}

func TestCopyOverlay_CopiesFiles(t *testing.T) {
	rigDir := t.TempDir()
	destDir := t.TempDir()

	// Create overlay directory with test files
	overlayDir := filepath.Join(rigDir, ".runtime", "overlay")
	if err := os.MkdirAll(overlayDir, 0755); err != nil {
		t.Fatalf("Failed to create overlay dir: %v", err)
	}

	// Create test files
	testFile1 := filepath.Join(overlayDir, "test1.txt")
	testFile2 := filepath.Join(overlayDir, "test2.txt")

	if err := os.WriteFile(testFile1, []byte("content1"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := os.WriteFile(testFile2, []byte("content2"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Copy overlay
	err := CopyOverlay(rigDir, destDir)
	if err != nil {
		t.Fatalf("CopyOverlay() error = %v", err)
	}

	// Verify files were copied
	destFile1 := filepath.Join(destDir, "test1.txt")
	destFile2 := filepath.Join(destDir, "test2.txt")

	content1, err := os.ReadFile(destFile1)
	if err != nil {
		t.Errorf("File test1.txt was not copied: %v", err)
	}
	if string(content1) != "content1" {
		t.Errorf("test1.txt content = %q, want %q", string(content1), "content1")
	}

	content2, err := os.ReadFile(destFile2)
	if err != nil {
		t.Errorf("File test2.txt was not copied: %v", err)
	}
	if string(content2) != "content2" {
		t.Errorf("test2.txt content = %q, want %q", string(content2), "content2")
	}
}

func TestCopyOverlay_PreservesPermissions(t *testing.T) {
	rigDir := t.TempDir()
	destDir := t.TempDir()

	// Create overlay directory with a file
	overlayDir := filepath.Join(rigDir, ".runtime", "overlay")
	if err := os.MkdirAll(overlayDir, 0755); err != nil {
		t.Fatalf("Failed to create overlay dir: %v", err)
	}

	testFile := filepath.Join(overlayDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("content"), 0755); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Copy overlay
	err := CopyOverlay(rigDir, destDir)
	if err != nil {
		t.Fatalf("CopyOverlay() error = %v", err)
	}

	// Verify permissions were preserved
	srcInfo, _ := os.Stat(testFile)
	destInfo, err := os.Stat(filepath.Join(destDir, "test.txt"))
	if err != nil {
		t.Fatalf("Failed to stat destination file: %v", err)
	}

	if srcInfo.Mode().Perm() != destInfo.Mode().Perm() {
		t.Errorf("Permissions not preserved: src=%v, dest=%v", srcInfo.Mode(), destInfo.Mode())
	}
}

func TestCopyOverlay_SkipsSubdirectories(t *testing.T) {
	rigDir := t.TempDir()
	destDir := t.TempDir()

	// Create overlay directory with a subdirectory
	overlayDir := filepath.Join(rigDir, ".runtime", "overlay")
	if err := os.MkdirAll(overlayDir, 0755); err != nil {
		t.Fatalf("Failed to create overlay dir: %v", err)
	}

	// Create a subdirectory
	subDir := filepath.Join(overlayDir, "subdir")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdirectory: %v", err)
	}

	// Create a file in the overlay root
	testFile := filepath.Join(overlayDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a file in the subdirectory
	subFile := filepath.Join(subDir, "sub.txt")
	if err := os.WriteFile(subFile, []byte("subcontent"), 0644); err != nil {
		t.Fatalf("Failed to create sub file: %v", err)
	}

	// Copy overlay
	err := CopyOverlay(rigDir, destDir)
	if err != nil {
		t.Fatalf("CopyOverlay() error = %v", err)
	}

	// Verify root file was copied
	if _, err := os.Stat(filepath.Join(destDir, "test.txt")); err != nil {
		t.Error("Root file should be copied")
	}

	// Verify subdirectory was NOT copied
	if _, err := os.Stat(filepath.Join(destDir, "subdir")); err == nil {
		t.Error("Subdirectory should not be copied")
	}
	if _, err := os.Stat(filepath.Join(destDir, "subdir", "sub.txt")); err == nil {
		t.Error("File in subdirectory should not be copied")
	}
}

func TestCopyOverlay_EmptyOverlay(t *testing.T) {
	rigDir := t.TempDir()
	destDir := t.TempDir()

	// Create empty overlay directory
	overlayDir := filepath.Join(rigDir, ".runtime", "overlay")
	if err := os.MkdirAll(overlayDir, 0755); err != nil {
		t.Fatalf("Failed to create overlay dir: %v", err)
	}

	// Copy overlay
	err := CopyOverlay(rigDir, destDir)
	if err != nil {
		t.Fatalf("CopyOverlay() error = %v", err)
	}

	// Should succeed without errors
}

func TestCopyOverlay_OverwritesExisting(t *testing.T) {
	rigDir := t.TempDir()
	destDir := t.TempDir()

	// Create overlay directory with test file
	overlayDir := filepath.Join(rigDir, ".runtime", "overlay")
	if err := os.MkdirAll(overlayDir, 0755); err != nil {
		t.Fatalf("Failed to create overlay dir: %v", err)
	}

	testFile := filepath.Join(overlayDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("new content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create existing file in destination with different content
	destFile := filepath.Join(destDir, "test.txt")
	if err := os.WriteFile(destFile, []byte("old content"), 0644); err != nil {
		t.Fatalf("Failed to create dest file: %v", err)
	}

	// Copy overlay
	err := CopyOverlay(rigDir, destDir)
	if err != nil {
		t.Fatalf("CopyOverlay() error = %v", err)
	}

	// Verify file was overwritten
	content, err := os.ReadFile(destFile)
	if err != nil {
		t.Fatalf("Failed to read dest file: %v", err)
	}
	if string(content) != "new content" {
		t.Errorf("File content = %q, want %q", string(content), "new content")
	}
}

func TestCopyFilePreserveMode(t *testing.T) {
	tmpDir := t.TempDir()

	// Create source file
	srcFile := filepath.Join(tmpDir, "src.txt")
	if err := os.WriteFile(srcFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create src file: %v", err)
	}

	// Copy file
	dstFile := filepath.Join(tmpDir, "dst.txt")
	err := copyFilePreserveMode(srcFile, dstFile)
	if err != nil {
		t.Fatalf("copyFilePreserveMode() error = %v", err)
	}

	// Verify content
	content, err := os.ReadFile(dstFile)
	if err != nil {
		t.Errorf("Failed to read dst file: %v", err)
	}
	if string(content) != "test content" {
		t.Errorf("Content = %q, want %q", string(content), "test content")
	}

	// Verify permissions
	srcInfo, _ := os.Stat(srcFile)
	dstInfo, err := os.Stat(dstFile)
	if err != nil {
		t.Fatalf("Failed to stat dst file: %v", err)
	}
	if srcInfo.Mode().Perm() != dstInfo.Mode().Perm() {
		t.Errorf("Permissions not preserved: src=%v, dest=%v", srcInfo.Mode(), dstInfo.Mode())
	}
}

func TestCopyFilePreserveMode_NonexistentSource(t *testing.T) {
	tmpDir := t.TempDir()

	srcFile := filepath.Join(tmpDir, "nonexistent.txt")
	dstFile := filepath.Join(tmpDir, "dst.txt")

	err := copyFilePreserveMode(srcFile, dstFile)
	if err == nil {
		t.Error("copyFilePreserveMode() with nonexistent source should return error")
	}
}

func TestEnsureGitignorePatterns_CreatesNewFile(t *testing.T) {
	tmpDir := t.TempDir()

	err := EnsureGitignorePatterns(tmpDir)
	if err != nil {
		t.Fatalf("EnsureGitignorePatterns() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, ".gitignore"))
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	// Check all required patterns are present (.beads/ intentionally excluded — see overlay.go)
	patterns := []string{".runtime/", ".claude/", ".logs/", "__pycache__/", "state.json"}
	for _, pattern := range patterns {
		if !containsLine(string(content), pattern) {
			t.Errorf(".gitignore missing pattern %q", pattern)
		}
	}

	// Fix #95: agent-internal plumbing patterns must be present.
	// These were leaking into rig commits and caused Fix #93.
	agentInternalPatterns := []string{
		".gt-agent",
		"typescript",
		"AGENTS.md",
		"project_label",
		"progress_report",
		"gt-agent-state.json",
	}
	for _, pattern := range agentInternalPatterns {
		if !containsLine(string(content), pattern) {
			t.Errorf("Fix #95 regression: .gitignore missing agent-internal pattern %q", pattern)
		}
	}

	// SPEC.md and README.md must NOT be in the ignore list — they are
	// user-authored content the polecat must be able to read from its
	// worktree. Initial Fix #95 wrongly ignored SPEC.md, which made
	// mockrig's project spec invisible after the rig reset.
	for _, userContent := range []string{"SPEC.md", "README.md"} {
		if containsLine(string(content), userContent) {
			t.Errorf("Fix #95 must NOT ignore user content %q (project spec / docs)", userContent)
		}
	}
}

func TestEnsureGitignorePatterns_AppendsToExisting(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing .gitignore with some content
	existing := "node_modules/\n*.log\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(existing), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	err := EnsureGitignorePatterns(tmpDir)
	if err != nil {
		t.Fatalf("EnsureGitignorePatterns() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, ".gitignore"))
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	// Should preserve existing content
	if !containsLine(string(content), "node_modules/") {
		t.Error("Existing pattern node_modules/ was removed")
	}

	// Should add header
	if !containsLine(string(content), "# Gas Town (added by gt)") {
		t.Error("Missing Gas Town header comment")
	}

	// Should add required patterns (.beads/ intentionally excluded — see overlay.go)
	patterns := []string{".runtime/", ".claude/", ".logs/", "__pycache__/", "state.json"}
	for _, pattern := range patterns {
		if !containsLine(string(content), pattern) {
			t.Errorf(".gitignore missing pattern %q", pattern)
		}
	}
}

func TestEnsureGitignorePatterns_SkipsExistingPatterns(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing .gitignore with some Gas Town patterns already.
	// The broader ".claude/" covers ".claude/commands/", so it should
	// not add the narrower pattern.
	existing := ".runtime/\n.claude/\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(existing), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	err := EnsureGitignorePatterns(tmpDir)
	if err != nil {
		t.Fatalf("EnsureGitignorePatterns() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, ".gitignore"))
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	// Should not duplicate existing patterns
	count := countOccurrences(string(content), ".runtime/")
	if count != 1 {
		t.Errorf(".runtime/ appears %d times, expected 1", count)
	}

	// .claude/ is now a direct required pattern — should not be duplicated
	claudeCount := countOccurrences(string(content), ".claude/")
	if claudeCount != 1 {
		t.Errorf(".claude/ appears %d times, expected 1", claudeCount)
	}

	// Should add missing patterns
	if !containsLine(string(content), ".logs/") {
		t.Error(".gitignore missing pattern .logs/")
	}
	if !containsLine(string(content), "__pycache__/") {
		t.Error(".gitignore missing pattern __pycache__/")
	}
	if !containsLine(string(content), "state.json") {
		t.Error(".gitignore missing pattern state.json")
	}

	// Regression guard: .beads/ must NOT be in required patterns.
	// Beads manages its own .beads/.gitignore via bd init.
	// Adding .beads/ here breaks bd sync. This has regressed twice
	// (PR #753, #966). If this test fails, you're about to break polecats.
	if containsLine(string(content), ".beads/") {
		t.Error(".gitignore must NOT contain .beads/ - beads manages its own .gitignore (see overlay.go comment)")
	}
}

func TestEnsureGitignorePatterns_RecognizesVariants(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing .gitignore with variant patterns (without trailing slash).
	// ".claude" (no trailing slash) should be recognized as covering ".claude/commands/".
	existing := ".runtime\n/.claude\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(existing), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	err := EnsureGitignorePatterns(tmpDir)
	if err != nil {
		t.Fatalf("EnsureGitignorePatterns() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, ".gitignore"))
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	// Should recognize variants and not add duplicates
	// .runtime (no slash) should count as .runtime/
	runtimeCount := countOccurrences(string(content), ".runtime")
	if runtimeCount > 1 {
		t.Errorf(".runtime appears %d times (variant detection failed)", runtimeCount)
	}

	// /.claude (leading slash, no trailing slash) should cover .claude/
	if containsLine(string(content), ".claude/") {
		t.Error(".claude/ should not be added when /.claude already covers it")
	}
}

func TestEnsureGitignorePatterns_AllPatternsPresent(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing .gitignore with all required patterns.
	// Fix #95: Includes the agent-internal patterns (.gt-agent,
	// typescript, AGENTS.md, SPEC.md, project_label, progress_report,
	// gt-agent-state.json) that were added to plug the
	// agent-plumbing-leaks-into-rig-repo hole.
	existing := ".runtime/\n.claude/\n.beads/\n.logs/\n__pycache__/\n.venv/\nvenv/\nstate.json\nCLAUDE.md\nCLAUDE.local.md\n" +
		".gt-agent\ntypescript\nAGENTS.md\nproject_label\nprogress_report\ngt-agent-state.json\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(existing), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	err := EnsureGitignorePatterns(tmpDir)
	if err != nil {
		t.Fatalf("EnsureGitignorePatterns() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, ".gitignore"))
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	// File should be unchanged (no header added)
	if containsLine(string(content), "# Gas Town") {
		t.Error("Should not add header when all patterns already present")
	}

	// Content should match original
	if string(content) != existing {
		t.Errorf("File was modified when it shouldn't be.\nGot: %q\nWant: %q", string(content), existing)
	}
}

func TestEnsureGitignorePatterns_NarrowPatternPresent(t *testing.T) {
	tmpDir := t.TempDir()

	// Create .gitignore with the exact required patterns.
	// Fix #95: also seeds the agent-internal patterns so this test
	// continues to exercise the "no changes needed" path.
	existing := ".runtime/\n.claude/\n.logs/\n__pycache__/\n.venv/\nvenv/\nstate.json\nCLAUDE.md\nCLAUDE.local.md\n" +
		".gt-agent\ntypescript\nAGENTS.md\nSPEC.md\nproject_label\nprogress_report\ngt-agent-state.json\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(existing), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	err := EnsureGitignorePatterns(tmpDir)
	if err != nil {
		t.Fatalf("EnsureGitignorePatterns() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, ".gitignore"))
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	// File should be unchanged
	if string(content) != existing {
		t.Errorf("File was modified when it shouldn't be.\nGot: %q\nWant: %q", string(content), existing)
	}
}

func TestEnsureGitignorePatterns_OldNarrowClaudeUpgraded(t *testing.T) {
	tmpDir := t.TempDir()

	// Simulate old installation with narrow .claude/commands/ pattern.
	// After upgrade, .claude/ (broad) should be added since .claude/commands/
	// does NOT cover .claude/ (the narrow is a subset, not a superset).
	existing := ".runtime/\n.claude/commands/\n.logs/\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(existing), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	err := EnsureGitignorePatterns(tmpDir)
	if err != nil {
		t.Fatalf("EnsureGitignorePatterns() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, ".gitignore"))
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	// .claude/ should be added (old .claude/commands/ doesn't cover it)
	if !containsLine(string(content), ".claude/") {
		t.Error(".claude/ should be added when only .claude/commands/ was present")
	}

	// __pycache__/ should be added
	if !containsLine(string(content), "__pycache__/") {
		t.Error("__pycache__/ should be added")
	}
}

func TestEnsureGitignorePatterns_UpgradePreservesBroadPattern(t *testing.T) {
	tmpDir := t.TempDir()

	// Simulate an existing installation that has .claude/ plus other Gas Town
	// patterns but is missing __pycache__/ (added later). After upgrade,
	// __pycache__/ should be appended.
	existing := "# Gas Town (added by gt)\n.runtime/\n.claude/\n.logs/\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".gitignore"), []byte(existing), 0644); err != nil {
		t.Fatalf("Failed to create .gitignore: %v", err)
	}

	err := EnsureGitignorePatterns(tmpDir)
	if err != nil {
		t.Fatalf("EnsureGitignorePatterns() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(tmpDir, ".gitignore"))
	if err != nil {
		t.Fatalf("Failed to read .gitignore: %v", err)
	}

	// __pycache__/ should be appended
	if !containsLine(string(content), "__pycache__/") {
		t.Error("__pycache__/ should be added during upgrade")
	}

	// Existing patterns should be preserved
	if !containsLine(string(content), ".runtime/") {
		t.Error(".runtime/ should be preserved")
	}
	if !containsLine(string(content), ".claude/") {
		t.Error(".claude/ should be preserved")
	}
}

func TestEnsureMayorRigGitHygiene_AppendsPatternsAndUntracksBeads(t *testing.T) {
	tmpDir := t.TempDir()
	if out, err := runGit(tmpDir, "init", "-q"); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if out, err := runGit(tmpDir, "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config email: %v: %s", err, out)
	}
	if out, err := runGit(tmpDir, "config", "user.name", "test"); err != nil {
		t.Fatalf("git config name: %v: %s", err, out)
	}
	beadsDir := filepath.Join(tmpDir, ".beads")
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("mkdir .beads: %v", err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "issues.jsonl"), []byte("{}\n"), 0644); err != nil {
		t.Fatalf("write issues: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "codeindex.json"), []byte("{}"), 0644); err != nil {
		t.Fatalf("write codeindex: %v", err)
	}
	if out, err := runGit(tmpDir, "add", "-A"); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	if out, err := runGit(tmpDir, "commit", "-q", "-m", "seed"); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}

	if err := EnsureMayorRigGitHygiene(tmpDir); err != nil {
		t.Fatalf("EnsureMayorRigGitHygiene: %v", err)
	}

	gitignore, err := os.ReadFile(filepath.Join(tmpDir, ".gitignore"))
	if err != nil {
		t.Fatalf("read .gitignore: %v", err)
	}
	for _, pattern := range []string{"codeindex.json", "*.db", "qa/implementation-progress.json"} {
		if !containsLine(string(gitignore), pattern) {
			t.Errorf(".gitignore missing %q", pattern)
		}
	}
	if containsLine(string(gitignore), ".beads/") {
		t.Error(".beads/ must not be in tracked .gitignore (bd sync); use local exclude")
	}

	if out, _ := runGit(tmpDir, "ls-files", ".beads"); strings.TrimSpace(out) != "" {
		t.Errorf(".beads should be untracked, got %q", out)
	}
	if out, _ := runGit(tmpDir, "ls-files", "codeindex.json"); strings.TrimSpace(out) != "" {
		t.Errorf("codeindex.json should be untracked, got %q", out)
	}
}

// TestGasTownLocalExcludePatterns_IncludesBeads verifies that the local exclude
// patterns include .beads/ (defense-in-depth for gas-7vg) while the gitignore
// patterns do NOT include .beads/ (regression guard).
func TestGasTownLocalExcludePatterns_IncludesBeads(t *testing.T) {
	localPatterns := gasTownLocalExcludePatterns()
	found := false
	for _, p := range localPatterns {
		if p == ".beads/" {
			found = true
			break
		}
	}
	if !found {
		t.Error("gasTownLocalExcludePatterns() must include .beads/ (gas-7vg defense-in-depth)")
	}

	// Regression guard: .gitignore patterns must NOT include .beads/
	gitignorePatterns := gasTownIgnorePatterns()
	for _, p := range gitignorePatterns {
		if p == ".beads/" {
			t.Error("gasTownIgnorePatterns() must NOT include .beads/ - that breaks bd sync (see overlay.go)")
		}
	}
}

// TestUntrackAgentInternals_RemovesTrackedFiles verifies Fix #95b:
// already-tracked agent-internal files (.gt-agent, typescript, etc.) are
// removed from the git index when EnsureLocalExcludePatterns runs,
// without deleting them from the working tree.
func TestUntrackAgentInternals_RemovesTrackedFiles(t *testing.T) {
	tmpDir := t.TempDir()

	// Initialize a git repo so `git ls-files` and `git rm --cached` work.
	if out, err := runGit(tmpDir, "init", "-q"); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if out, err := runGit(tmpDir, "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config email: %v: %s", err, out)
	}
	if out, err := runGit(tmpDir, "config", "user.name", "test"); err != nil {
		t.Fatalf("git config name: %v: %s", err, out)
	}

	// Stage and commit a typescript file plus a legitimate user file.
	tsPath := filepath.Join(tmpDir, "typescript")
	if err := os.WriteFile(tsPath, []byte("simulated script(1) output\n"), 0644); err != nil {
		t.Fatalf("writing typescript: %v", err)
	}
	gtAgentPath := filepath.Join(tmpDir, ".gt-agent")
	if err := os.WriteFile(gtAgentPath, []byte(`{"role":"refinery"}`), 0644); err != nil {
		t.Fatalf("writing .gt-agent: %v", err)
	}
	userPath := filepath.Join(tmpDir, "user-code.py")
	if err := os.WriteFile(userPath, []byte("print('hi')\n"), 0644); err != nil {
		t.Fatalf("writing user file: %v", err)
	}
	if out, err := runGit(tmpDir, "add", "-A"); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	if out, err := runGit(tmpDir, "commit", "-q", "-m", "initial"); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}

	// Sanity: both plumbing files are tracked.
	if out, err := runGit(tmpDir, "ls-files", "typescript"); err != nil || strings.TrimSpace(out) != "typescript" {
		t.Fatalf("expected typescript tracked: out=%q err=%v", out, err)
	}
	if out, err := runGit(tmpDir, "ls-files", ".gt-agent"); err != nil || strings.TrimSpace(out) != ".gt-agent" {
		t.Fatalf("expected .gt-agent tracked: out=%q err=%v", out, err)
	}

	// Run EnsureLocalExcludePatterns — this triggers untrackAgentInternals.
	if err := EnsureLocalExcludePatterns(tmpDir); err != nil {
		t.Fatalf("EnsureLocalExcludePatterns: %v", err)
	}

	// typescript and .gt-agent should now be UNTRACKED.
	if out, _ := runGit(tmpDir, "ls-files", "typescript"); strings.TrimSpace(out) != "" {
		t.Errorf("Fix #95b: typescript should be untracked, got: %q", out)
	}
	if out, _ := runGit(tmpDir, "ls-files", ".gt-agent"); strings.TrimSpace(out) != "" {
		t.Errorf("Fix #95b: .gt-agent should be untracked, got: %q", out)
	}

	// But files should still exist on disk (working tree intact).
	if _, err := os.Stat(tsPath); err != nil {
		t.Errorf("typescript file should still exist on disk: %v", err)
	}
	if _, err := os.Stat(gtAgentPath); err != nil {
		t.Errorf(".gt-agent file should still exist on disk: %v", err)
	}

	// User file should remain tracked (untracking is path-specific).
	if out, _ := runGit(tmpDir, "ls-files", "user-code.py"); strings.TrimSpace(out) != "user-code.py" {
		t.Errorf("user-code.py should remain tracked, got: %q", out)
	}
}

// TestUntrackAgentInternals_NoopWhenNotTracked verifies the function is
// safe to call on a worktree where the plumbing files were never tracked
// in the first place (the common case).
func TestUntrackAgentInternals_NoopWhenNotTracked(t *testing.T) {
	tmpDir := t.TempDir()

	if out, err := runGit(tmpDir, "init", "-q"); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if out, err := runGit(tmpDir, "config", "user.email", "test@example.com"); err != nil {
		t.Fatalf("git config email: %v: %s", err, out)
	}
	if out, err := runGit(tmpDir, "config", "user.name", "test"); err != nil {
		t.Fatalf("git config name: %v: %s", err, out)
	}
	// Empty repo, no commits, no tracked plumbing files. Should not error.
	if err := EnsureLocalExcludePatterns(tmpDir); err != nil {
		t.Fatalf("EnsureLocalExcludePatterns on empty repo: %v", err)
	}
}

// runGit is a tiny helper for Fix #95b tests — wraps exec.Command for git
// invocations against a specific worktree.
func runGit(worktree string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", worktree}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// Helper functions

func containsLine(content, pattern string) bool {
	for _, line := range splitLines(content) {
		if line == pattern {
			return true
		}
	}
	return false
}

func countOccurrences(content, pattern string) int {
	count := 0
	for _, line := range splitLines(content) {
		if line == pattern {
			count++
		}
	}
	return count
}

func splitLines(content string) []string {
	var lines []string
	start := 0
	for i, c := range content {
		if c == '\n' {
			lines = append(lines, content[start:i])
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, content[start:])
	}
	return lines
}
