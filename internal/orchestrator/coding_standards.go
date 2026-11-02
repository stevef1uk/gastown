package orchestrator

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxCodingStandardsContextBytes = 900

// FormatCodingStandardsBlock returns concise, technology-specific Town Hall coding standards
// for the active phase and implement bead. Go projects never receive Python standards and vice versa.
func FormatCodingStandardsBlock(townRoot, rig string, v WorkflowValidation) string {
	stacks := DetectActiveCodingStacks(townRoot, rig, v)
	if len(stacks) == 0 {
		return ""
	}
	scoped := v.ForActivePhase()
	vars := scoped.PromptVars()
	if rig != "" {
		vars["rig"] = rig
	}
	if strings.TrimSpace(vars["layout_root"]) == "" {
		layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(scoped.LayoutRoot)), "/")
		if layout == "" {
			layout = "."
		}
		vars["layout_root"] = layout
	}

	var sections []string
	totalBytes := 0
	for _, stack := range stacks {
		content := loadTownStandardForStack(townRoot, stack, vars)
		if content == "" {
			continue
		}
		if totalBytes+len(content) > maxCodingStandardsContextBytes {
			remaining := maxCodingStandardsContextBytes - totalBytes
			if remaining < 120 {
				break
			}
			content = truncateAtLineBoundary(content, remaining)
		}
		if content != "" {
			sections = append(sections, content)
			totalBytes += len(content)
		}
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

// DetectActiveCodingStacks determines the technology stack(s) relevant to the current
// implement bead (when one is active) or the active delivery phase.
// Canonical stack names: "go", "python", "typescript", "sql", "docker".
func DetectActiveCodingStacks(townRoot, rig string, v WorkflowValidation) []string {
	scoped := v.ForActivePhase()
	activeBeadPath := activeImplementBeadPathForContext(townRoot, rig, scoped)
	if activeBeadPath != "" {
		if beadStacks := stacksForFilePath(activeBeadPath, scoped); len(beadStacks) > 0 {
			return beadStacks
		}
	}
	return stacksForValidation(scoped)
}

func activeImplementBeadPathForContext(townRoot, rig string, v WorkflowValidation) string {
	if townRoot == "" || rig == "" || len(v.RequiredFiles) == 0 {
		return ""
	}
	var next *PlanBead
	var err error
	if nextOpenImplementBeadHook != nil {
		next, err = nextOpenImplementBeadHook(townRoot, rig, v)
	} else {
		next, err = NextOpenImplementBead(townRoot, rig, v)
	}
	if err != nil || next == nil {
		return ""
	}
	return NormalizeBeadPathForLayout(
		ExtractPathFromBeadTitle(next.Title, v.BeadTitleContains),
		v.LayoutRoot,
	)
}

func stacksForFilePath(relPath string, v WorkflowValidation) []string {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(relPath)))
	base := filepath.Base(lower)
	switch {
	case strings.HasSuffix(lower, ".go") || base == "go.mod" || base == "go.sum":
		out := []string{"go"}
		if isStoreOrSchemaCodePath(lower) && phaseHasSQLFiles(v) {
			out = append(out, "sql")
		}
		return out
	case strings.HasSuffix(lower, ".py") || base == "requirements.txt" || base == "pyproject.toml":
		out := []string{"python"}
		if isStoreOrSchemaCodePath(lower) && phaseHasSQLFiles(v) {
			out = append(out, "sql")
		}
		return out
	case strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".tsx") ||
		strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".jsx") ||
		strings.HasSuffix(lower, ".mjs") || strings.HasSuffix(lower, ".cjs") ||
		strings.HasSuffix(lower, ".html") || strings.HasSuffix(lower, ".css") ||
		base == "package.json" || base == "tsconfig.json":
		return []string{"typescript"}
	case strings.HasSuffix(lower, ".sql"):
		return []string{"sql"}
	case base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") ||
		base == "docker-compose.yml" || base == "docker-compose.yaml" ||
		base == "compose.yml" || base == "compose.yaml":
		return []string{"docker"}
	default:
		return nil
	}
}

func stacksForValidation(v WorkflowValidation) []string {
	var out []string
	addUnique := func(stack string) {
		for _, s := range out {
			if s == stack {
				return
			}
		}
		out = append(out, stack)
	}

	if WorkflowUsesGo(v) {
		addUnique("go")
	} else if WorkflowUsesPython(v) {
		addUnique("python")
	}
	if WorkflowUsesNodeJS(v) || phaseHasWebFiles(v) {
		addUnique("typescript")
	}
	if phaseHasSQLFiles(v) {
		addUnique("sql")
	}
	if WorkflowUsesDocker(v) || phaseHasDockerFiles(v) {
		addUnique("docker")
	}
	return out
}

func isStoreOrSchemaCodePath(lowerPath string) bool {
	return strings.Contains(lowerPath, "store") ||
		strings.Contains(lowerPath, "schema") ||
		strings.Contains(lowerPath, "db") ||
		strings.Contains(lowerPath, "sqlite") ||
		strings.Contains(lowerPath, "repo")
}

func phaseHasWebFiles(v WorkflowValidation) bool {
	for _, f := range v.RequiredFiles {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		if strings.HasSuffix(lower, ".ts") || strings.HasSuffix(lower, ".tsx") ||
			strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".jsx") ||
			strings.HasSuffix(lower, ".html") {
			return true
		}
	}
	return false
}

func phaseHasSQLFiles(v WorkflowValidation) bool {
	for _, f := range v.RequiredFiles {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		if strings.HasSuffix(lower, ".sql") {
			return true
		}
	}
	return false
}

func phaseHasDockerFiles(v WorkflowValidation) bool {
	for _, f := range v.RequiredFiles {
		base := filepath.Base(strings.ToLower(filepath.ToSlash(strings.TrimSpace(f))))
		if base == "dockerfile" || strings.HasPrefix(base, "dockerfile.") ||
			base == "docker-compose.yml" || base == "docker-compose.yaml" ||
			base == "compose.yml" || base == "compose.yaml" {
			return true
		}
	}
	return false
}

func loadTownStandardForStack(townRoot, stack string, vars map[string]string) string {
	stack = strings.ToLower(strings.TrimSpace(stack))
	if stack == "" {
		return ""
	}
	rel := filepath.Join("standards", stack+".md")
	if townRoot != "" {
		diskPath := filepath.Join(townRoot, TownAssetsSubdir, rel)
		if data, err := os.ReadFile(diskPath); err == nil && len(data) > 0 {
			return strings.TrimSpace(SubstituteVars(string(data), vars))
		}
	}
	embedPath := filepath.ToSlash(filepath.Join("town", rel))
	if data, err := fs.ReadFile(townAssets, embedPath); err == nil && len(data) > 0 {
		return strings.TrimSpace(SubstituteVars(string(data), vars))
	}
	return ""
}

func truncateAtLineBoundary(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := s[:maxBytes]
	if idx := strings.LastIndex(cut, "\n"); idx > 0 {
		return strings.TrimSpace(cut[:idx])
	}
	return strings.TrimSpace(cut)
}
