package orchestrator

import (
	"path/filepath"
	"strings"
)

// CompileErrorPathsIncludingClosedDeps augments go error paths with earlier required_files
// whose implement beads are closed when the failure looks cross-package.
func CompileErrorPathsIncludingClosedDeps(townRoot, rig, activeBeadPath string, errorPaths []string, cmdOutput string, v WorkflowValidation) []string {
	activeBeadPath = filepath.ToSlash(strings.TrimSpace(activeBeadPath))
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || p == activeBeadPath || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range errorPaths {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range GoTestFailureProductionPaths(cmdOutput, v.LayoutRoot) {
		add(p)
	}
	if activeBeadPath == "" || !verifyOutputSuggestsCrossFile(cmdOutput) {
		return out
	}
	deps := EarlierRequiredFilesForBead(activeBeadPath, v.RequiredFiles)
	for _, p := range GoTestFailureProductionPaths(cmdOutput, v.LayoutRoot) {
		deps = appendUniquePath(deps, p)
	}
	for _, dep := range deps {
		if !strings.HasSuffix(strings.ToLower(dep), ".go") {
			continue
		}
		if closedOnly, err := ImplementPathHasOnlyClosedBeads(townRoot, rig, dep, v); err != nil || !closedOnly {
			continue
		}
		// Include closed deps in source context when cited or implicated; reopen *hints* are softer (see FormatClosedDependencyCompileHints).
		if outputMentionsPath(cmdOutput, dep, v.LayoutRoot) || GoCompileOutputCitesFile(cmdOutput, dep, v.LayoutRoot) {
			add(dep)
		}
	}
	return out
}

func compileOutputSuggestsCrossPackage(cmdOutput string) bool {
	lower := strings.ToLower(cmdOutput)
	return strings.Contains(lower, "undefined:") ||
		strings.Contains(lower, "cannot use") ||
		strings.Contains(lower, " imports") ||
		strings.Contains(lower, "wrong type") ||
		strings.Contains(lower, "not enough arguments") ||
		strings.Contains(lower, "too many arguments")
}

func outputMentionsPath(cmdOutput, beadPath, layoutRoot string) bool {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" {
		return false
	}
	lower := strings.ToLower(cmdOutput)
	if strings.Contains(lower, strings.ToLower(beadPath)) {
		return true
	}
	rel := beadPath
	layoutRoot = strings.Trim(layoutRoot, "/")
	if layoutRoot != "" && strings.HasPrefix(beadPath, layoutRoot+"/") {
		rel = strings.TrimPrefix(beadPath, layoutRoot+"/")
	}
	if rel != "" && strings.Contains(lower, strings.ToLower(rel)) {
		return true
	}
	pkgDir := filepath.ToSlash(filepath.Dir(rel))
	if pkgDir != "" && pkgDir != "." && strings.Contains(lower, strings.ToLower(pkgDir)) {
		return true
	}
	if pkg := filepath.Base(pkgDir); pkg != "" && pkg != "." {
		if strings.Contains(lower, strings.ToLower(pkg)+".") || strings.Contains(lower, "/"+strings.ToLower(pkg)+"/") {
			return true
		}
	}
	return strings.Contains(lower, strings.ToLower(filepath.Base(beadPath)))
}

func appendUniquePath(paths []string, p string) []string {
	p = filepath.ToSlash(strings.TrimSpace(p))
	if p == "" {
		return paths
	}
	for _, existing := range paths {
		if filepath.ToSlash(existing) == p {
			return paths
		}
	}
	return append(paths, p)
}
