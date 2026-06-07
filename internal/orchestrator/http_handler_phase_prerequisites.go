package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var handlerTestMainOpenRE = regexp.MustCompile(`(?s)func TestMain\(m \*testing\.M\) \{`)

// ActivePhaseNeedsHandlerHTTPTests reports whether the scoped phase verify exercises handler packages.
func ActivePhaseNeedsHandlerHTTPTests(v WorkflowValidation) bool {
	scoped := v.ForActivePhase()
	q := strings.ToLower(strings.TrimSpace(scoped.QAVerifyCommand))
	if q == "" {
		return false
	}
	for _, f := range scoped.RequiredFiles {
		if IsHTTPHandlerImplementPath(f) || IsHTTPHandlerTestPath(f) {
			return strings.Contains(q, "internal/api") || strings.Contains(q, "handlers")
		}
	}
	return false
}

// EnsureHandlerPhasePrerequisitesLog scaffolds missing web/ assets and repairs handler TestMain
// when the active delivery phase runs handler HTTP tests but web files are scheduled later.
func EnsureHandlerPhasePrerequisitesLog(townRoot, rig string, v WorkflowValidation) (string, error) {
	if townRoot == "" || rig == "" || !ProfileRequiresWebAssets(v) {
		return "", nil
	}
	if !ActivePhaseNeedsHandlerHTTPTests(v) {
		return "", nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var parts []string

	created, err := ensureMinimalWebAssetsForHandlerTests(rigDir, v)
	if err != nil {
		return "", err
	}
	if len(created) > 0 {
		parts = append(parts, "scaffolded web for handler tests: "+joinStrings(created, ", "))
	}

	fixed, err := ensureHandlerTestMainStoreDB(rigDir, v)
	if err != nil {
		return "", err
	}
	if len(fixed) > 0 {
		parts = append(parts, "patched handler TestMain store.DB: "+joinStrings(fixed, ", "))
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, "; "), nil
}

func ensureMinimalWebAssetsForHandlerTests(rigDir string, v WorkflowValidation) ([]string, error) {
	missing := MissingWebAssetPaths(rigDir, v)
	if len(missing) == 0 {
		return nil, nil
	}
	var created []string
	for _, rel := range missing {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		abs := filepath.Join(rigDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return created, err
		}
		content := minimalWebAssetContent(rel)
		if content == "" {
			continue
		}
		if _, err := os.Stat(abs); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return created, err
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			return created, err
		}
		created = append(created, rel)
	}
	return created, nil
}

func minimalWebAssetContent(rel string) string {
	base := strings.ToLower(filepath.Base(rel))
	switch {
	case strings.HasSuffix(base, "index.html"):
		return "<!DOCTYPE html>\n<html lang=\"en\"><head><meta charset=\"utf-8\"><title>Link Shelf</title></head><body><h1>Link Shelf</h1></body></html>\n"
	case strings.HasSuffix(base, ".css"):
		return "body { font-family: system-ui, sans-serif; }\n"
	case strings.HasSuffix(base, ".js"):
		return "// Link Shelf UI — expand in web-static delivery phase.\n"
	default:
		return ""
	}
}

func ensureHandlerTestMainStoreDB(rigDir string, v WorkflowValidation) ([]string, error) {
	testRel := handlerTestRelForHarness(rigDir, v)
	if testRel == "" {
		return nil, nil
	}
	abs := filepath.Join(rigDir, filepath.FromSlash(testRel))
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	src := string(data)
	if !handlerTestMainOpenRE.MatchString(src) || strings.Contains(src, "store.DB") {
		return nil, nil
	}
	if !strings.Contains(src, "InitSchema") || !strings.Contains(src, `sql.Open("sqlite3"`) {
		return nil, nil
	}
	const blockEnd = "\tif err := store.InitSchema(db); err != nil {"
	idx := strings.Index(src, blockEnd)
	if idx < 0 {
		return nil, nil
	}
	rest := src[idx+len(blockEnd):]
	closeIdx := strings.Index(rest, "\n\t}")
	if closeIdx < 0 {
		return nil, nil
	}
	insertAt := idx + len(blockEnd) + closeIdx + len("\n\t}")
	out := src[:insertAt] + "\n\tstore.DB = db\n" + src[insertAt:]
	if string(data) == out {
		return nil, nil
	}
	if err := os.WriteFile(abs, []byte(out), 0o644); err != nil {
		return nil, err
	}
	return []string{testRel}, nil
}

// TryAutoFixHandlerTestStoreDB patches handlers_test.go TestMain when verify reports store DB not initialized.
func TryAutoFixHandlerTestStoreDB(mayorRigDir string, v WorkflowValidation, cmdOutput string) ([]string, error) {
	if !strings.Contains(cmdOutput, "store DB not initialized") {
		return nil, nil
	}
	return ensureHandlerTestMainStoreDB(mayorRigDir, v)
}

func handlerTestRelForHarness(rigDir string, v WorkflowValidation) string {
	_, testRel := HandlerHTTPPathsForAutoFix(rigDir, v)
	if testRel != "" {
		return testRel
	}
	for _, want := range v.UnionRequiredFiles() {
		if !IsHTTPHandlerImplementPath(want) {
			continue
		}
		corr := CorrelatedTestPathForSource(want, v)
		if corr == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(rigDir, filepath.FromSlash(corr))); err == nil {
			return corr
		}
	}
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" {
		return ""
	}
	fallback := layout + "/internal/api/handlers_test.go"
	if _, err := os.Stat(filepath.Join(rigDir, filepath.FromSlash(fallback))); err == nil {
		return fallback
	}
	return ""
}
