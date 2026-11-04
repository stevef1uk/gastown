package orchestrator

import (
	"path/filepath"
	"regexp"
	"strings"
)

var (
	pythonReworkExceptionRE   = regexp.MustCompile(`(?m)^\s*(?:\[WebServer\]\s*)?(?:E\s+)?((?:[A-Za-z_][A-Za-z0-9_.]*)?(?:Error|Exception)):[^\r\n]*`)
	pythonReworkFrameRE       = regexp.MustCompile(`(?m)File ["']([^"'\r\n]+\.py)["'], line \d+`)
	pythonReworkPytestFrameRE = regexp.MustCompile(`(?m)^\s*(?:E\s+)?([^\s:]+\.py):\d+:`)
	pythonReworkProviderRE    = regexp.MustCompile(`\(([^()\r\n]+\.py)\)`)
	pythonReworkImportRE      = regexp.MustCompile(`cannot import name ["'][^"']+["'] from ["']([^"']+)["']`)
	pythonReworkModuleRE      = regexp.MustCompile(`(?:No module named|module) ["']([^"']+)["']`)
)

func completedPhasePythonFailure(rigDir string, v WorkflowValidation, output string) (int, []string, string) {
	if !v.HasPhasedDelivery() || v.ActivePhaseIndex() <= 0 {
		return -1, nil, ""
	}
	exceptions := pythonReworkExceptionRE.FindAllStringSubmatchIndex(output, -1)
	if len(exceptions) == 0 {
		return -1, nil, ""
	}
	last := exceptions[len(exceptions)-1]
	exception := output[last[0]:last[1]]
	kind := output[last[2]:last[3]]
	switch kind {
	case "ImportError", "ModuleNotFoundError", "SyntaxError", "IndentationError", "TabError", "NameError", "AttributeError", "TypeError":
	default:
		return -1, nil, ""
	}
	var path string
	if kind == "ImportError" || kind == "ModuleNotFoundError" || kind == "AttributeError" {
		module := pythonReworkImportRE.FindStringSubmatch(exception)
		if len(module) == 0 {
			module = pythonReworkModuleRE.FindStringSubmatch(exception)
		}
		if len(module) > 0 {
			if isPythonStdlib(module[1]) || loadDeclaredThirdPartyModules(rigDir).matches(module[1], ".py") {
				return -1, nil, ""
			}
			if provider := pythonReworkProviderRE.FindStringSubmatch(exception); len(provider) > 0 {
				path = ownedPythonDiagnosticPath(rigDir, provider[1], v)
			} else {
				path = pythonModuleOwnedPath(module[1], v)
			}
			if path == "" {
				return -1, nil, ""
			}
		} else if kind != "AttributeError" {
			return -1, nil, ""
		}
	}
	if path == "" {
		frames := pythonReworkFrameRE.FindAllStringSubmatch(output[:last[0]], -1)
		if len(frames) == 0 {
			frames = pythonReworkPytestFrameRE.FindAllStringSubmatch(output[:last[0]], -1)
		}
		if len(frames) == 0 {
			return -1, nil, ""
		}
		path = ownedPythonDiagnosticPath(rigDir, frames[len(frames)-1][1], v)
	}
	idx, paths := earlierPhaseDiagnosticPaths(v, []string{path})
	return idx, paths, output
}

func ownedPythonDiagnosticPath(rigDir, path string, v WorkflowValidation) string {
	if filepath.IsAbs(path) {
		rel, err := filepath.Rel(rigDir, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return ""
		}
		path = rel
	}
	path = filepath.ToSlash(filepath.Clean(path))
	for _, file := range v.UnionRequiredFiles() {
		file = filepath.ToSlash(filepath.Clean(file))
		if strings.HasSuffix(file, ".py") && (path == file || filepath.ToSlash(filepath.Join(v.LayoutRootDir(), path)) == file) && !pythonEnvironmentPath(file, v) {
			return file
		}
	}
	return ""
}

func pythonModuleOwnedPath(module string, v WorkflowValidation) string {
	stem := strings.ReplaceAll(module, ".", "/")
	var match string
	for _, file := range v.UnionRequiredFiles() {
		file = filepath.ToSlash(filepath.Clean(file))
		if pythonEnvironmentPath(file, v) {
			continue
		}
		for _, suffix := range []string{stem + ".py", stem + "/__init__.py"} {
			if file == suffix || strings.HasSuffix(file, "/"+suffix) {
				if match != "" && match != file {
					return ""
				}
				match = file
			}
		}
	}
	return match
}

func pythonEnvironmentPath(path string, v WorkflowValidation) bool {
	wrapped := "/" + filepath.ToSlash(path) + "/"
	for _, segment := range []string{"site-packages", "dist-packages", ".venv", "venv"} {
		if strings.Contains(wrapped, "/"+segment+"/") {
			return true
		}
	}
	venv := strings.Trim(filepath.ToSlash(v.PythonVenvRelDir()), "/")
	return venv != "" && strings.HasPrefix(filepath.ToSlash(path), venv+"/")
}
