package orchestrator

import (
	"path/filepath"
	"sort"
	"strings"
)

// ProjectComponent is an independently installed package root. Detection is
// compositional: test harness packages do not replace the application's stack.
type ProjectComponent struct {
	Root      string
	Toolchain string
	Manifest  string
}

func WorkflowComponents(v WorkflowValidation) []ProjectComponent {
	var result []ProjectComponent
	seen := map[string]bool{}
	for _, file := range v.RequiredFiles {
		file = filepath.ToSlash(filepath.Clean(file))
		root := filepath.ToSlash(filepath.Dir(file))
		kind := ""
		switch filepath.Base(file) {
		case "go.mod":
			kind = "go"
		case "pyproject.toml", "requirements.txt":
			kind = "python"
		case "package.json":
			kind = "nodejs"
		}
		key := root + ":" + kind
		if kind != "" && !seen[key] {
			seen[key] = true
			result = append(result, ProjectComponent{root, kind, file})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Root == result[j].Root {
			return result[i].Toolchain < result[j].Toolchain
		}
		return result[i].Root < result[j].Root
	})
	return result
}

func shellQuoteComponentPath(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

func componentSetupCommand(c ProjectComponent, files []string) string {
	prefix := "(cd " + shellQuoteComponentPath(c.Root) + " && "
	switch c.Toolchain {
	case "go":
		return prefix + "go mod download)"
	case "python":
		for _, f := range files {
			if filepath.ToSlash(filepath.Clean(f)) == filepath.ToSlash(filepath.Join(c.Root, "uv.lock")) {
				return prefix + "uv sync)"
			}
		}
		if filepath.Base(c.Manifest) == "requirements.txt" {
			return prefix + "python3 -m venv .venv && .venv/bin/python -m pip install -r requirements.txt)"
		}
		return prefix + "python3 -m venv .venv && .venv/bin/python -m pip install -e '.[dev]')"
	case "nodejs":
		scopedFiles := []string{}
		for _, file := range files {
			if filepath.ToSlash(filepath.Dir(file)) == c.Root {
				scopedFiles = append(scopedFiles, file)
			}
		}
		pm := "npm"
		for _, file := range scopedFiles {
			if filepath.ToSlash(filepath.Clean(file)) == filepath.ToSlash(filepath.Join(c.Root, "pnpm-lock.yaml")) {
				pm = "pnpm"
				break
			}
			if filepath.ToSlash(filepath.Clean(file)) == filepath.ToSlash(filepath.Join(c.Root, "yarn.lock")) {
				pm = "yarn"
			}
		}
		return prefix + nodeInstallCommand(pm, scopedFiles) + ")"
	}
	return ""
}

func mixedComponentSetup(v WorkflowValidation) string {
	var commands []string
	for _, component := range WorkflowComponents(v) {
		commands = append(commands, componentSetupCommand(component, v.RequiredFiles))
	}
	return strings.Join(commands, " && ")
}
