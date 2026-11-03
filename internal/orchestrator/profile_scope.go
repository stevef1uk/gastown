package orchestrator

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var declaredPhaseHeadingRE = regexp.MustCompile(`(?i)^#{1,6}\s+Phase\s+[0-9]+\s*[:.\-—–]+\s*(.+?)\s*#*\s*$`)
var phaseSlugRE = regexp.MustCompile(`[^a-z0-9]+`)
var verifyCommandSeparatorRE = regexp.MustCompile(`&&|;|\n`)
var nodeProductionBuildRE = regexp.MustCompile(`^(npm\s+run|pnpm(?:\s+run)?|yarn(?:\s+run)?)\s+build\b|^(next|vite|webpack|ng)\s+build\b`)
var containerBuildRE = regexp.MustCompile(`^docker\s+build\b|^docker(?:-compose|\s+compose)\b[^;&|]*\b(build|up\b[^;&|]*--build)\b`)

func phaseScopeKey(s string) string {
	return strings.Trim(phaseSlugRE.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// DeclaredSpecPhaseHeadings recognizes explicit numbered Markdown phases even
// without a wrapping "Delivery phases" section. Examples and diagrams in code
// fences are not declarations. File assignment remains the indexer's job.
func DeclaredSpecPhaseHeadings(spec string) []DeliveryPhase {
	var phases []DeliveryPhase
	seen := map[string]bool{}
	fence := ""
	for _, line := range strings.Split(spec, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			marker := line[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}
			continue
		}
		if fence != "" {
			continue
		}
		m := declaredPhaseHeadingRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		id := phaseScopeKey(m[1])
		if id != "" && !seen[id] {
			seen[id] = true
			phases = append(phases, DeliveryPhase{ID: id, Title: m[1], SpecFocus: m[1]})
		}
	}
	return phases
}

func missingDeclaredPhases(declared, actual []DeliveryPhase) []string {
	have := map[string]bool{}
	for _, p := range actual {
		have[phaseScopeKey(p.ID)] = true
		have[phaseScopeKey(p.Title)] = true
	}
	var missing []string
	for _, p := range declared {
		if !have[phaseScopeKey(p.ID)] {
			missing = append(missing, p.Title)
		}
	}
	return missing
}

// Buildable Node projects are identified by declared build configuration, not
// by a directory named frontend (plain static assets need no Node build).
func nodeBuildGateProblems(v WorkflowValidation) []string {
	files := v.UnionRequiredFiles()
	roots := map[string]bool{}
	for _, f := range files {
		base := filepath.Base(f)
		if strings.HasPrefix(base, "next.config.") || strings.HasPrefix(base, "vite.config.") || strings.HasPrefix(base, "webpack.config.") || base == "angular.json" {
			roots[filepath.ToSlash(filepath.Dir(f))] = true
		}
	}
	var problems []string
	var sortedRoots []string
	for root := range roots {
		sortedRoots = append(sortedRoots, root)
	}
	sort.Strings(sortedRoots)
	for _, root := range sortedRoots {
		manifest := root + "/package.json"
		if root == "." {
			manifest = "package.json"
		}
		if !containsString(files, manifest) {
			problems = append(problems, fmt.Sprintf("buildable Node project %q has no declared package.json", root))
		}
		for _, p := range v.DeliveryPhases {
			hasSource := false
			for _, f := range p.RequiredFiles {
				if root != "." && !strings.HasPrefix(f, root+"/") {
					continue
				}
				if scaffoldHarnessPath(f) || strings.Contains(f, ".config.") {
					continue
				}
				switch filepath.Ext(f) {
				case ".ts", ".tsx", ".js", ".jsx", ".vue", ".svelte":
					hasSource = true
				}
			}
			// A container build is also an executable build gate. File checks,
			// backend imports, typechecking, and test enumeration are not.
			if hasSource && !hasNodeProjectBuildGate(p.QAVerifyCommand, root) {
				problems = append(problems, fmt.Sprintf("phase %q delivers source in buildable Node project %q but has no build gate; verify that project's production build in addition to other stacks", p.ID, root))
			}
		}
	}
	return problems
}

func hasNodeProjectBuildGate(command, root string) bool {
	cwd := "."
	for _, part := range verifyCommandSeparatorRE.Split(command, -1) {
		part = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(part), "("))
		if strings.HasPrefix(part, "cd ") {
			dir := strings.Trim(strings.TrimSpace(strings.TrimPrefix(part, "cd ")), "\"'")
			cwd = filepath.ToSlash(filepath.Clean(filepath.Join(cwd, dir)))
			continue
		}
		if containerBuildRE.MatchString(part) {
			return true
		}
		if cwd == root && nodeProductionBuildRE.MatchString(part) {
			return true
		}
	}
	return false
}
