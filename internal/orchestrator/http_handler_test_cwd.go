package orchestrator

import (
	"path/filepath"
	"strings"
)

// GoTestOutputSuggestsHandlerWebCwd404 reports httptest 404 on GET / or static (profile matchers + legacy).
func GoTestOutputSuggestsHandlerWebCwd404(townRoot, rig string, v WorkflowValidation, cmdOutput string) bool {
	if LoadHTTPImplementationProfile(townRoot, rig, v).GoTestOutputSuggestsHandlerWebCwd404(cmdOutput) {
		return true
	}
	return goTestOutputSuggestsHandlerWebCwd404Legacy(cmdOutput)
}

// FormatHandlerRoot404ServeWebHint suggests using serveWebFile for GET / (profile text).
func FormatHandlerRoot404ServeWebHint(townRoot, rig, activeBeadPath, cmdOutput string, v WorkflowValidation) string {
	return LoadHTTPImplementationProfile(townRoot, rig, v).FormatHandlerRoot404ServeWebHint(activeBeadPath, cmdOutput, v)
}

// FormatHandlerTestCwdHint explains handler_test 404 when web/ exists but cwd is wrong (profile text).
func FormatHandlerTestCwdHint(townRoot, rig, activeBeadPath, cmdOutput string, v WorkflowValidation) string {
	return LoadHTTPImplementationProfile(townRoot, rig, v).FormatHandlerTestCwdHint(activeBeadPath, cmdOutput, v)
}

// HandlerTestMissingModuleChdirIssues flags httptest handler tests without module-root chdir.
func HandlerTestMissingModuleChdirIssues(townRoot, rig, relPath, content string, v WorkflowValidation) []string {
	return LoadHTTPImplementationProfile(townRoot, rig, v).HandlerTestMissingModuleChdirIssues(relPath, content, v)
}

// ChdirExprToModuleRootFromTest returns a filepath.Join("..", …) expression from a test file path.
func ChdirExprToModuleRootFromTest(beadPath, layoutRoot string) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	pkgDir := filepath.Dir(beadPath)
	if layout != "" && strings.HasPrefix(pkgDir, layout+"/") {
		pkgDir = strings.TrimPrefix(pkgDir, layout+"/")
	}
	segments := strings.Split(strings.Trim(pkgDir, "/"), "/")
	if len(segments) == 0 {
		return `"."`
	}
	parts := make([]string, len(segments))
	for i := range segments {
		parts[i] = `".."`
	}
	return "filepath.Join(" + strings.Join(parts, ", ") + ")"
}

// handlerTestPathForHints maps an active handlers.go bead to its *_test.go for cwd guidance.
func handlerTestPathForHints(activeBeadPath, cmdOutput string, v WorkflowValidation) string {
	activeBeadPath = filepath.ToSlash(strings.TrimSpace(activeBeadPath))
	if IsHTTPHandlerTestPath(activeBeadPath) {
		return activeBeadPath
	}
	if IsHTTPHandlerImplementPath(activeBeadPath) {
		return CorrelatedTestPathForSource(activeBeadPath, v)
	}
	if strings.Contains(cmdOutput, "handlers_test.go") {
		layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
		if layout != "" {
			return layout + "/internal/api/handlers_test.go"
		}
	}
	return activeBeadPath
}
