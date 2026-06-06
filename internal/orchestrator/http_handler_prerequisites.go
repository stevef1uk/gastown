package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var handlerStatic404TestRE = regexp.MustCompile(`(?i)TestServe(Index|Static)\b`)

// ProfileRequiresWebAssets reports whether the workflow expects a web/ tree for HTTP serving.
func ProfileRequiresWebAssets(v WorkflowValidation) bool {
	if workflowHasGoWebAndServer(v) {
		return true
	}
	for _, f := range v.RequiredFilesForSmokeScope() {
		if strings.Contains(filepath.ToSlash(f), "/web/") {
			return true
		}
	}
	return false
}

// MissingWebAssetPaths returns layout-relative web paths from the profile that are absent on disk.
func MissingWebAssetPaths(rigDir string, v WorkflowValidation) []string {
	if !ProfileRequiresWebAssets(v) {
		return nil
	}
	var missing []string
	seen := map[string]bool{}
	add := func(rel string) {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || seen[rel] {
			return
		}
		seen[rel] = true
		abs := filepath.Join(rigDir, filepath.FromSlash(rel))
		if info, err := os.Stat(abs); err != nil || info.IsDir() {
			missing = append(missing, rel)
		}
	}
	for _, rel := range profileWebAssetPaths(v) {
		add(rel)
	}
	return missing
}

func profileWebAssetPaths(v WorkflowValidation) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range append(append([]string(nil), v.RequiredFiles...), v.UnionRequiredFiles()...) {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" || seen[f] || !strings.Contains(f, "/web/") {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// WebAssetsReady reports whether required web files exist under mayor/rig.
func WebAssetsReady(rigDir string, v WorkflowValidation) bool {
	return len(MissingWebAssetPaths(rigDir, v)) == 0
}

// ValidateHTTPHandlerBeadPrerequisites blocks handler/handler-test work until web assets exist.
func ValidateHTTPHandlerBeadPrerequisites(mayorRigDir, relPath string, v WorkflowValidation) error {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" || !ProfileRequiresWebAssets(v) {
		return nil
	}
	if !IsHTTPHandlerImplementPath(relPath) && !IsHTTPHandlerTestPath(relPath) {
		return nil
	}
	missing := MissingWebAssetPaths(mayorRigDir, v)
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("%s: implement **web/** assets first (%s) — handler tests call ServeIndex/ServeStatic against the real web/ tree; changing handler paths to ../../web will not fix missing files",
		relPath, strings.Join(missing, ", "))
}

// FormatHTTPHandlerPrerequisiteBlock warns when the active bead is handlers* but web/ is missing.
func FormatHTTPHandlerPrerequisiteBlock(townRoot, rig, beadPath string, v WorkflowValidation) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if strings.Contains(beadPath, "/web/") {
		return ""
	}
	if !IsHTTPHandlerImplementPath(beadPath) && !IsHTTPHandlerTestPath(beadPath) {
		return ""
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	missing := MissingWebAssetPaths(rigDir, v)
	if len(missing) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Web assets required before handlers (system)\n")
	b.WriteString("These profile paths are **missing on disk**:\n")
	for _, p := range missing {
		b.WriteString("- `")
		b.WriteString(p)
		b.WriteString("`\n")
	}
	b.WriteString("\nDo **not** close this handler bead or chase 404s by changing `web/` path prefixes in handlers.go. ")
	b.WriteString("Implement the **web/** implement beads first (or **WRITE:** the missing files), then handlers.\n")
	b.WriteString("Use paths relative to the module root (`web/index.html`, `web/app.js`) — tests run with `cd ")
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	b.WriteString(layout)
	b.WriteString("`.\n")
	return strings.TrimSpace(b.String())
}

// GoTestOutputSuggestsHandlerStatic404 reports httptest 404 on ServeIndex/ServeStatic.
func GoTestOutputSuggestsHandlerStatic404(cmdOutput string) bool {
	if !goTestOutputSuggestsFailure(cmdOutput) {
		return false
	}
	if !strings.Contains(cmdOutput, "got 404") && !strings.Contains(cmdOutput, "StatusNotFound") {
		return false
	}
	return handlerStatic404TestRE.MatchString(cmdOutput) ||
		(strings.Contains(cmdOutput, "handlers_test.go") &&
			(strings.Contains(cmdOutput, "TestServeIndex") || strings.Contains(cmdOutput, "TestServeStatic")))
}

// FormatHandlerStatic404Hint explains 404 handler test failures (missing web/ or wrong bead order).
func FormatHandlerStatic404Hint(townRoot, rig, activeBeadPath, cmdOutput string, v WorkflowValidation) string {
	if !GoTestOutputSuggestsHandlerStatic404(cmdOutput) {
		return ""
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	missing := MissingWebAssetPaths(rigDir, v)
	var b strings.Builder
	b.WriteString("### Handler test 404 (static routes)\n")
	if len(missing) > 0 {
		b.WriteString("**Cause:** `web/` files are missing — tests expect real files on disk, not path tweaks in handlers.go.\n")
		b.WriteString("**Fix:** implement missing web paths first: ")
		b.WriteString(strings.Join(missing, ", "))
		b.WriteString(". Then use `web/index.html` and `web/<file>` in handlers (module cwd = layout root).\n")
	} else {
		b.WriteString("**Cause:** handlers do not serve the architecture paths from the real `web/` tree (wrong relative path or not using `web/`).\n")
		b.WriteString("**Fix:** align ServeIndex/ServeStatic with architecture (typically `filepath.Join(\"web\", \"index.html\")` and `filepath.Join(\"web\", trimmed)` for `/static/{file}`). ")
		b.WriteString("Do **not** use `../../web` unless architecture says so; tests run from the module directory.\n")
	}
	if activeBeadPath != "" {
		b.WriteString("\nActive bead: `")
		b.WriteString(activeBeadPath)
		b.WriteString("`.\n")
	}
	return strings.TrimSpace(b.String())
}
