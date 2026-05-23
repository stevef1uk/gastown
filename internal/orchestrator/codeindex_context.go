package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	codeindexImpactMaxBytes = 2200
	codeindexIndexName      = "codeindex.json"
)

// CodeindexEnabled reports whether optional codeindex integration is on (binary in PATH, not disabled).
func CodeindexEnabled() bool {
	if strings.TrimSpace(os.Getenv("GT_CODEINDEX")) == "0" || strings.TrimSpace(os.Getenv("CODEINDEX")) == "0" {
		return false
	}
	_, err := exec.LookPath("codeindex")
	return err == nil
}

// CodeindexIndexPath is where gastown stores the dependency index for a rig worktree.
func CodeindexIndexPath(mayorRigDir string) string {
	return filepath.Join(mayorRigDir, codeindexIndexName)
}

// codeindexAnalyzeRoot returns the subtree passed to `codeindex analyze` (usually layout_root).
func codeindexAnalyzeRoot(mayorRigDir string, v WorkflowValidation) string {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" || layout == "." {
		return mayorRigDir
	}
	return filepath.Join(mayorRigDir, layout)
}

// RefreshCodeindexIndex builds codeindex.json + inline symbols when missing or stale.
func RefreshCodeindexIndex(mayorRigDir string, v WorkflowValidation) (log string, err error) {
	if !CodeindexEnabled() {
		return "", nil
	}
	mayorRigDir = strings.TrimSpace(mayorRigDir)
	if mayorRigDir == "" {
		return "", nil
	}
	analyzeRoot := codeindexAnalyzeRoot(mayorRigDir, v)
	if _, err := os.Stat(analyzeRoot); err != nil {
		return "", nil
	}
	indexPath := CodeindexIndexPath(mayorRigDir)
	if !codeindexIndexNeedsRefresh(indexPath, analyzeRoot) {
		return "", nil
	}
	analyze := exec.Command("codeindex", "analyze", analyzeRoot, "--output", indexPath)
	analyze.Dir = mayorRigDir
	if out, runErr := analyze.CombinedOutput(); runErr != nil {
		return "", fmt.Errorf("codeindex analyze: %w: %s", runErr, strings.TrimSpace(string(out)))
	}
	symbols := exec.Command("codeindex", "symbols", analyzeRoot, "--inline", "--index", indexPath)
	symbols.Dir = mayorRigDir
	if out, runErr := symbols.CombinedOutput(); runErr != nil {
		return "codeindex analyze ok; symbols failed: " + strings.TrimSpace(string(out)), runErr
	}
	return "codeindex: built " + codeindexIndexName + " (analyze + inline symbols)", nil
}

func codeindexIndexNeedsRefresh(indexPath, analyzeRoot string) bool {
	info, err := os.Stat(indexPath)
	if err != nil {
		return true
	}
	newest, err := newestSourceMtime(analyzeRoot)
	if err != nil || newest.IsZero() {
		return false
	}
	return info.ModTime().Before(newest)
}

func newestSourceMtime(root string) (time.Time, error) {
	var newest time.Time
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			base := info.Name()
			if base == ".git" || base == "node_modules" || base == ".venv" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		switch ext {
		case ".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".rb", ".java", ".php", ".vue":
		default:
			return nil
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest, err
}

// FormatCodeindexContextForBead injects blast-radius impact for the active implement path.
func FormatCodeindexContextForBead(mayorRigDir, beadPath string, v WorkflowValidation) string {
	if !CodeindexEnabled() {
		return ""
	}
	beadPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(beadPath)), v.LayoutRoot)
	if beadPath == "" {
		return ""
	}
	mayorRigDir = strings.TrimSpace(mayorRigDir)
	indexPath := CodeindexIndexPath(mayorRigDir)
	if _, err := os.Stat(indexPath); err != nil {
		return ""
	}
	impactPath := beadPath
	if layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/"); layout != "" && strings.HasPrefix(impactPath, layout+"/") {
		impactPath = strings.TrimPrefix(impactPath, layout+"/")
	}
	cmd := exec.Command("codeindex", "impact", impactPath, "--index", indexPath)
	cmd.Dir = mayorRigDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		if text == "" {
			return ""
		}
		return strings.TrimSpace("### Codeindex (impact lookup failed)\n" + truncateCodeindexText(text, 800))
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Codeindex blast radius (optional — read before large EDITs)\n")
	b.WriteString("Install: `pip install codeindex`. Refresh: `codeindex analyze` in mayor/rig.\n\n")
	b.WriteString("```\n")
	b.WriteString(truncateCodeindexText(text, codeindexImpactMaxBytes))
	b.WriteString("\n```\n")
	b.WriteString("\nPrefer **Dependency packages** and **Current file on disk** for APIs; use codeindex to see who imports this file.\n")
	return strings.TrimSpace(b.String())
}

func truncateCodeindexText(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "\n…"
}
