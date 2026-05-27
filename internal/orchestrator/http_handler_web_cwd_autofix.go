package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	serveIndexGetwdOpenRE = regexp.MustCompile(`(?s)\s*wd, err := os\.Getwd\(\)\s*
	if err != nil \{\s*
		http\.Error\(w, [^,]+, http\.StatusInternalServerError\)\s*
		return\s*
	\}\s*
	f, err := os\.Open\(filepath\.Join\(wd,\s*"[^"]+",\s*"index\.html"\)\)`)

	fragileTestChdirRE = regexp.MustCompile(`filepath\.Join\(\s*"\.\."\s*,\s*"\.\."`)

	fragileAbsRepoRootRE = regexp.MustCompile(`filepath\.Abs\(filepath\.Join\(\s*"\.\."\s*,\s*"\.\."\s*\)\)`)
)

// ShouldAutoFixHandlerWebCwd404 reports whether verify output and on-disk web/ justify an automatic cwd fix.
func ShouldAutoFixHandlerWebCwd404(mayorRigDir, townRoot, rig string, v WorkflowValidation, cmdOutput string) bool {
	if !ProfileRequiresWebAssets(v) {
		return false
	}
	if mayorRigDir == "" {
		mayorRigDir = filepath.Join(townRoot, rig, "mayor", "rig")
	}
	if !WebAssetsReady(mayorRigDir, v) {
		return false
	}
	return GoTestOutputSuggestsHandlerWebCwd404(townRoot, rig, v, cmdOutput) ||
		GoTestOutputSuggestsHandlerStatic404(cmdOutput)
}

func goTestOutputSuggestsHandlerWebCwd404Legacy(cmdOutput string) bool {
	if !goTestOutputSuggestsFailure(cmdOutput) {
		return false
	}
	if !strings.Contains(cmdOutput, "handlers_test.go") {
		return false
	}
	if strings.Contains(cmdOutput, "GET / returned 404") {
		return true
	}
	if strings.Contains(cmdOutput, "TestServeIndex") || strings.Contains(cmdOutput, "TestServeStatic") ||
		strings.Contains(cmdOutput, "TestRegisterHandlers") {
		return strings.Contains(cmdOutput, "404") ||
			strings.Contains(cmdOutput, "StatusNotFound") ||
			strings.Contains(cmdOutput, "got 404")
	}
	return false
}

// HandlerHTTPPathsForAutoFix returns layout-relative handlers.go and handlers_test.go when present.
func HandlerHTTPPathsForAutoFix(mayorRigDir string, v WorkflowValidation) (handlersRel, testRel string) {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	candidates := []string{
		filepath.ToSlash(filepath.Join(layout, "internal/api/handlers.go")),
	}
	for _, want := range v.RequiredFiles {
		want = filepath.ToSlash(strings.TrimSpace(want))
		if IsHTTPHandlerImplementPath(want) {
			candidates = append([]string{want}, candidates...)
		}
	}
	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		abs := filepath.Join(mayorRigDir, filepath.FromSlash(c))
		if _, err := os.Stat(abs); err == nil {
			handlersRel = c
			testRel = CorrelatedTestPathForSource(c, v.LayoutRoot)
			return handlersRel, testRel
		}
	}
	return "", ""
}

// TryAutoFixHandlerWebCwd404 patches handler cwd issues (Getwd vs module root, fragile test chdir).
// Returns layout-relative paths that were modified.
func TryAutoFixHandlerWebCwd404(mayorRigDir, townRoot, rig string, v WorkflowValidation, cmdOutput string) ([]string, error) {
	if !ShouldAutoFixHandlerWebCwd404(mayorRigDir, townRoot, rig, v, cmdOutput) {
		return nil, nil
	}
	handlersRel, testRel := HandlerHTTPPathsForAutoFix(mayorRigDir, v)
	if handlersRel == "" {
		return nil, nil
	}
	prof := LoadHTTPImplementationProfile(townRoot, rig, v)
	webDir := strings.TrimSpace(prof.WebDiskDir)
	if webDir == "" {
		webDir = "web"
	}
	var fixed []string
	if testRel != "" {
		if changed, err := autoFixHandlerTestCwd(filepath.Join(mayorRigDir, filepath.FromSlash(testRel)), webDir); err != nil {
			return fixed, err
		} else if changed {
			fixed = append(fixed, testRel)
		}
	}
	if changed, err := autoFixHandlersServeIndexGetwd(filepath.Join(mayorRigDir, filepath.FromSlash(handlersRel)), webDir); err != nil {
		return fixed, err
	} else if changed {
		if !containsPath(fixed, handlersRel) {
			fixed = append(fixed, handlersRel)
		}
	}
	return fixed, nil
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

func autoFixHandlersServeIndexGetwd(absPath, webDir string) (bool, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	src := string(data)
	if !strings.Contains(src, "func serveIndex") || !strings.Contains(src, "os.Getwd()") {
		return false, nil
	}
	if !serveIndexGetwdOpenRE.MatchString(src) {
		return false, nil
	}
	repl := fmt.Sprintf(`_, filename, _, _ := runtime.Caller(0)
	indexPath := filepath.Join(filepath.Dir(filename), "..", "..", %q, "index.html")
	f, err := os.Open(indexPath)`, webDir)
	out := serveIndexGetwdOpenRE.ReplaceAllString(src, repl)
	out = ensureGoImport(out, "runtime")
	return writeIfChanged(absPath, data, []byte(out))
}

func autoFixHandlerTestCwd(absPath, webDir string) (bool, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	src := string(data)
	if strings.Contains(src, "findModuleRootWithWeb") {
		return false, nil
	}
	if !fragileTestChdirRE.MatchString(src) {
		return false, nil
	}
	if !strings.Contains(src, "testing") {
		return false, nil
	}
	out := src
	if !strings.Contains(out, "func findModuleRootWithWeb") {
		out = injectAfterTestImports(out, handlerWebCwdTestHelperBlock(webDir))
	}
	if fragileAbsRepoRootRE.MatchString(out) {
		out = fragileAbsRepoRootRE.ReplaceAllString(out, "findModuleRootWithWeb()")
	}
	if strings.Contains(out, "func changeDirToRepoRoot") && strings.Contains(out, "findModuleRootWithWeb()") {
		out = strings.Replace(out, `func changeDirToRepoRoot(t *testing.T) {
	root, err := findModuleRootWithWeb()
	if err != nil {
		t.Fatalf("cannot determine repo root: %v", err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatalf("cannot chdir to repo root %s: %v", root, err)
	}
}`, `func changeDirToRepoRoot(t *testing.T) {
	chdirToModuleRoot(t)
}`, 1)
	}
	out = ensureGoImport(out, "fmt")
	return writeIfChanged(absPath, data, []byte(out))
}

func handlerWebCwdTestHelperBlock(webDir string) string {
	return fmt.Sprintf(`
// chdirToModuleRoot sets cwd to the Go module root (go.mod + %s/ on disk).
func chdirToModuleRoot(t *testing.T) {
	t.Helper()
	dir, err := findModuleRootWithWeb()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
}

func findModuleRootWithWeb() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, %q, "index.html")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("module root not found (go.mod and %s/index.html) from %%s", wd)
		}
		dir = parent
	}
}
`, webDir, webDir, webDir)
}

func injectAfterTestImports(src, block string) string {
	const pkg = "package api"
	idx := strings.Index(src, pkg)
	if idx < 0 {
		return src + block
	}
	rest := src[idx+len(pkg):]
	importEnd := strings.Index(rest, ")")
	if importEnd < 0 || !strings.Contains(rest[:importEnd+1], "import") {
		return src[:idx+len(pkg)] + "\n" + block + rest
	}
	pos := idx + len(pkg) + importEnd + 1
	return src[:pos] + block + src[pos:]
}

func ensureGoImport(src, pkg string) string {
	if strings.Contains(src, `"`+pkg+`"`) {
		return src
	}
	const open = "import ("
	if idx := strings.Index(src, open); idx >= 0 {
		return src[:idx+len(open)] + "\n\t\"" + pkg + "\"" + src[idx+len(open):]
	}
	if idx := strings.Index(src, "import "); idx >= 0 {
		// single-line import block — add second import via parens
		lineEnd := strings.Index(src[idx:], "\n")
		if lineEnd < 0 {
			lineEnd = len(src) - idx
		}
		old := src[idx : idx+lineEnd]
		if strings.Contains(old, "(") {
			return src
		}
		inner := strings.TrimPrefix(strings.TrimSpace(old), "import ")
		return src[:idx] + "import (\n\t" + inner + "\n\t\"" + pkg + "\"\n)" + src[idx+lineEnd:]
	}
	return src
}

func writeIfChanged(absPath string, before []byte, after []byte) (bool, error) {
	if string(before) == string(after) {
		return false, nil
	}
	if err := os.WriteFile(absPath, after, 0644); err != nil {
		return false, err
	}
	return true, nil
}
