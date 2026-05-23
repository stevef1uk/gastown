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

// codeindexLayoutRelativePath strips layout_root from a bead path for the codeindex CLI.
func codeindexLayoutRelativePath(beadPath string, v WorkflowValidation) string {
	impactPath := filepath.ToSlash(strings.TrimSpace(beadPath))
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout != "" && strings.HasPrefix(impactPath, layout+"/") {
		impactPath = strings.TrimPrefix(impactPath, layout+"/")
	}
	return impactPath
}

// codeindexImpactCandidates returns paths to try with `codeindex impact`.
// Go rigs index packages (internal/store), not individual .go files — package path first.
func codeindexImpactCandidates(beadPath string, v WorkflowValidation) []string {
	rel := codeindexLayoutRelativePath(beadPath, v)
	if rel == "" {
		return nil
	}
	var candidates []string
	if WorkflowUsesGo(v) && strings.HasSuffix(rel, ".go") {
		if pkg := GoBuildRelPackage(v.LayoutRoot, beadPath); pkg != "" {
			candidates = append(candidates, pkg)
		}
	}
	candidates = append(candidates, rel)
	seen := map[string]bool{}
	var out []string
	for _, c := range candidates {
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

func runCodeindexImpact(mayorRigDir, indexPath, impactPath string) (string, error) {
	cmd := exec.Command("codeindex", "impact", impactPath, "--index", indexPath)
	cmd.Dir = mayorRigDir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
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
	candidates := codeindexImpactCandidates(beadPath, v)
	var text string
	var failText string
	for _, impactPath := range candidates {
		out, err := runCodeindexImpact(mayorRigDir, indexPath, impactPath)
		if err == nil && out != "" {
			text = out
			break
		}
		if out != "" {
			failText = out
		}
	}
	if text == "" {
		if failText == "" {
			return ""
		}
		return strings.TrimSpace("### Codeindex (impact lookup failed)\n" + truncateCodeindexText(failText, 800))
	}
	var b strings.Builder
	b.WriteString("### Codeindex blast radius (optional — read before large EDITs)\n")
	b.WriteString("Install: `pip install codeindex`. Refresh: `codeindex analyze` in mayor/rig.\n\n")
	b.WriteString("```\n")
	b.WriteString(truncateCodeindexText(text, codeindexImpactMaxBytes))
	b.WriteString("\n```\n")
	if WorkflowUsesGo(v) && strings.HasSuffix(beadPath, ".go") {
		b.WriteString("\nPrefer **Dependency packages** and **Current file on disk** for APIs; codeindex shows who imports this **package**.\n")
	} else {
		b.WriteString("\nPrefer **Dependency packages** and **Current file on disk** for APIs; use codeindex to see who imports this file.\n")
	}
	return strings.TrimSpace(b.String())
}

func truncateCodeindexText(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return s[:max] + "\n…"
}
