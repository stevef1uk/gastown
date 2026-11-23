package orchestrator

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	pythonPipInstallRE = regexp.MustCompile(`(?im)(?:^|[;&|]\s*|\bRUN\s+)(?:python3?\s+-m\s+)?pip(?:3)?\s+install[^\n]*`)
	pythonDepSpecRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*(?:\[[A-Za-z0-9_.,-]+\])?(?:[!<>=~].*)?$`)
	pythonKnownDepRE   = regexp.MustCompile(`(?i)\b(fastapi|starlette|uvicorn|gunicorn|hypercorn|flask|django|httpx|pytest|jinja2|pydantic)\b`)
	pythonTestClientRE = regexp.MustCompile(`(?i)(testclient|test_client|httpx)`)
	pythonVersionRE    = regexp.MustCompile(`[<>=!~]`)
)

var pythonKnownDeps = map[string]bool{
	"fastapi":   true,
	"starlette": true,
	"uvicorn":   true,
	"gunicorn":  true,
	"hypercorn": true,
	"flask":     true,
	"django":    true,
	"httpx":     true,
	"pytest":    true,
	"jinja2":    true,
	"pydantic":  true,
}

func EnsurePythonRuntimeDepsLog(townRoot, rig string, v WorkflowValidation) (string, error) {
	if townRoot == "" || rig == "" || !pythonWorkflowHasServerEntry(v) {
		return "", nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	req := filepath.ToSlash(strings.TrimSpace(v.RequirementsFilePath()))
	if req == "" || !strings.HasSuffix(strings.ToLower(req), "requirements.txt") {
		req = filepath.Join(v.LayoutRootDir(), "requirements.txt")
	}
	target := filepath.Join(rigDir, filepath.FromSlash(req))
	pinned := pythonPinnedDepsFromRig(rigDir)
	existing := readPythonRequirementSpecs(target)
	required := pythonRequiredDeps(rigDir, v, pinned, existing)
	merged := make(map[string]string, len(required)+len(existing))
	for name, spec := range required {
		if old, ok := existing[name]; ok && !pythonVersionRE.MatchString(spec) && pythonVersionRE.MatchString(old) {
			spec = old
		}
		merged[name] = spec
	}
	for name, spec := range existing {
		if _, ok := merged[name]; !ok {
			merged[name] = spec
		}
	}
	changed, err := writePythonRequirements(target, merged)
	if err != nil {
		return "", err
	}
	if !changed {
		return "", nil
	}
	return fmt.Sprintf("python runtime deps: updated %s (%s)", req, strings.Join(sortedPythonDepNames(merged), ", ")), nil
}

func ensurePythonRequirementsFile(files []string, layout string) []string {
	req := "requirements.txt"
	if layout = strings.Trim(strings.TrimSpace(filepath.ToSlash(layout)), "/"); layout != "" && layout != "." {
		req = filepath.ToSlash(filepath.Join(layout, "requirements.txt"))
	}
	for _, f := range files {
		if strings.EqualFold(strings.TrimSpace(filepath.Base(filepath.ToSlash(strings.TrimSpace(f)))), "requirements.txt") {
			return files
		}
	}
	return append(files, req)
}

func pythonRequiredDeps(rigDir string, v WorkflowValidation, pinned, existing map[string]string) map[string]string {
	deps := map[string]string{}
	add := func(name string) {
		name = pythonDepName(name)
		if !pythonKnownDeps[name] {
			return
		}
		if _, ok := deps[name]; ok {
			return
		}
		if spec, ok := pinned[name]; ok && spec != "" {
			deps[name] = spec
			return
		}
		if spec, ok := existing[name]; ok && spec != "" {
			deps[name] = spec
			return
		}
		deps[name] = name
	}
	add("pytest")

	commands := []string{v.QAVerifyCommand, v.UnittestCommandHint()}
	for _, p := range v.DeliveryPhases {
		commands = append(commands, p.QAVerifyCommand)
	}
	for _, cmd := range commands {
		scanPythonDependencyText(cmd, add)
	}
	for _, rel := range []string{"SPEC.md", "architecture.md"} {
		scanPythonDependencyText(readFileIfExists(filepath.Join(rigDir, rel)), add)
	}
	for _, f := range v.UnionRequiredFiles() {
		rel := filepath.ToSlash(strings.TrimSpace(f))
		if rel == "" || strings.Contains(rel, "node_modules/") || strings.Contains(rel, ".venv/") {
			continue
		}
		scanPythonDependencyText(readFileIfExists(filepath.Join(rigDir, filepath.FromSlash(rel))), add)
	}
	for _, path := range pythonDockerfiles(rigDir) {
		scanPythonDependencyText(readFileIfExists(path), add)
	}
	for name := range pinned {
		add(name)
	}
	return deps
}

func scanPythonDependencyText(text string, add func(string)) {
	if text == "" {
		return
	}
	for _, match := range pythonKnownDepRE.FindAllStringSubmatch(text, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}
	if pythonTestClientRE.MatchString(text) {
		add("httpx")
	}
}

func pythonPinnedDepsFromRig(rigDir string) map[string]string {
	out := map[string]string{}
	for _, path := range pythonDockerfiles(rigDir) {
		content := readFileIfExists(path)
		for _, line := range pythonPipInstallRE.FindAllString(content, -1) {
			for _, token := range strings.Fields(line) {
				spec := strings.Trim(token, `"'`)
				if !pythonDepSpecRE.MatchString(spec) {
					continue
				}
				name := pythonDepName(spec)
				if pythonKnownDeps[name] {
					out[name] = spec
				}
			}
		}
	}
	return out
}

func pythonDockerfiles(rigDir string) []string {
	var paths []string
	_ = filepath.WalkDir(rigDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "node_modules" || name == ".venv" || name == ".git" || name == ".gastown" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(strings.ToLower(d.Name()), "dockerfile") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths
}

func readPythonRequirementSpecs(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		if !pythonDepSpecRE.MatchString(line) {
			continue
		}
		out[pythonDepName(line)] = line
	}
	return out
}

func writePythonRequirements(path string, deps map[string]string) (bool, error) {
	if len(deps) == 0 {
		return false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	lines := []string(nil)
	if err == nil {
		lines = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	declared := map[string]bool{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "-") {
			continue
		}
		if pythonDepSpecRE.MatchString(trimmed) {
			declared[pythonDepName(trimmed)] = true
		}
	}
	changed := false
	for _, name := range sortedPythonDepNames(deps) {
		if declared[name] {
			continue
		}
		lines = append(lines, deps[name])
		changed = true
	}
	if !changed && err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		return false, err
	}
	return true, nil
}

func sortedPythonDepNames(deps map[string]string) []string {
	names := make([]string, 0, len(deps))
	for name := range deps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func pythonDepName(spec string) string {
	spec = strings.TrimSpace(spec)
	if i := strings.IndexAny(spec, "[<>=!~;@ "); i >= 0 {
		spec = spec[:i]
	}
	return strings.ToLower(strings.ReplaceAll(spec, "_", "-"))
}

func readFileIfExists(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if len(data) > 2<<20 {
		data = data[:2<<20]
	}
	return string(data)
}
