package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var goModRequireLineRE = regexp.MustCompile(`(?m)^require\s+(\S+)\s+(\S+)`)
var goModFenceRE = regexp.MustCompile("(?is)##\\s*Module[^\\n]*\\n+```(?P<body>[^`]+)```")
var goModBlockRequireLineRE = regexp.MustCompile(`(?m)^\s+(\S+)\s+(v[\w./+-]+|\S+)\s*$`)

// RequiredGoModRequireDirectives parses require lines from SPEC.md (Module section).
func RequiredGoModRequireDirectives(rigDir string) []string {
	specPath := filepath.Join(rigDir, "SPEC.md")
	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, m := range goModRequireLineRE.FindAllStringSubmatch(string(data), -1) {
		if len(m) < 3 {
			continue
		}
		line := strings.TrimSpace("require " + m[1] + " " + strings.Trim(m[2], "`"))
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		out = append(out, line)
	}
	return out
}

// GoModFileHasRequire reports whether go.mod lists module at version (single-line or block require).
func GoModFileHasRequire(data []byte, module, version string) bool {
	module = strings.TrimSpace(module)
	version = strings.TrimSpace(strings.Trim(version, "`"))
	if module == "" || version == "" {
		return false
	}
	content := string(data)
	if strings.Contains(content, module+" "+version) || strings.Contains(content, module+"@"+version) {
		return true
	}
	for _, m := range goModRequireLineRE.FindAllStringSubmatch(content, -1) {
		if len(m) >= 3 && m[1] == module && strings.Trim(m[2], "`") == version {
			return true
		}
	}
	for _, m := range goModBlockRequireLineRE.FindAllStringSubmatch(content, -1) {
		if len(m) >= 3 && m[1] == module && strings.Trim(m[2], "`") == version {
			return true
		}
	}
	return false
}

// RepairGoModRequiresFromSpec re-adds SPEC.md require directives removed by go mod tidy on empty modules.
func RepairGoModRequiresFromSpec(rigDir string, v WorkflowValidation) (string, error) {
	if !WorkflowUsesGo(v) {
		return "", nil
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	modPath := filepath.Join(rigDir, layout, "go.mod")
	data, err := os.ReadFile(modPath)
	if err != nil {
		return "", nil
	}
	var missing []string
	for _, req := range RequiredGoModRequireDirectives(rigDir) {
		parts := strings.Fields(strings.TrimPrefix(req, "require "))
		if len(parts) < 2 {
			continue
		}
		mod, ver := parts[0], strings.Trim(parts[1], "`")
		if GoModFileHasRequire(data, mod, ver) {
			continue
		}
		missing = append(missing, fmt.Sprintf("go mod edit -require=%s@%s", mod, ver))
	}
	if len(missing) == 0 {
		return "", nil
	}
	verify := GoShellCDClause(rigDir, v.LayoutRoot) + strings.Join(missing, " && ")
	cmd := exec.Command("/bin/bash", "-c", verify)
	cmd.Dir = rigDir
	cmd.Env = os.Environ()
	out, runErr := cmd.CombinedOutput()
	if runErr != nil {
		return "", fmt.Errorf("repair go.mod requires: %w: %s", runErr, strings.TrimSpace(string(out)))
	}
	return "restored SPEC require(s) in " + filepath.ToSlash(filepath.Join(layout, "go.mod")), nil
}

// ValidateGoModFile checks go.mod against SPEC.md module name and require directives.
func ValidateGoModFile(rigDir string, v WorkflowValidation) error {
	if !WorkflowUsesGo(v) {
		return nil
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	modPath := filepath.Join(rigDir, layout, "go.mod")
	data, err := os.ReadFile(modPath)
	if err != nil {
		return fmt.Errorf("go.mod missing at %s", filepath.ToSlash(filepath.Join(layout, "go.mod")))
	}
	specPath := filepath.Join(rigDir, "SPEC.md")
	specData, _ := os.ReadFile(specPath)
	specDoc := string(specData)
	canonical := canonicalGoModule(specDoc, v)
	if canonical != "" {
		if m := goModModuleLineRE.FindStringSubmatch(string(data)); len(m) >= 2 {
			if strings.TrimSpace(m[1]) != canonical {
				return fmt.Errorf("go.mod module %q must be %q per SPEC.md", m[1], canonical)
			}
		} else {
			return fmt.Errorf("go.mod missing module %q line from SPEC.md", canonical)
		}
	}
	for _, req := range RequiredGoModRequireDirectives(rigDir) {
		parts := strings.Fields(strings.TrimPrefix(req, "require "))
		if len(parts) < 2 {
			continue
		}
		mod, ver := parts[0], strings.Trim(parts[1], "`")
		if !GoModFileHasRequire(data, mod, ver) {
			return fmt.Errorf("go.mod missing SPEC requirement %q — READ SPEC.md Module section and EDIT go.mod", mod+" "+ver)
		}
	}
	return nil
}

// GoModBlockFromSpec returns the fenced Module block body from SPEC.md, or "".
func GoModBlockFromSpec(rigDir string) string {
	data, err := os.ReadFile(filepath.Join(rigDir, "SPEC.md"))
	if err != nil {
		return ""
	}
	m := goModFenceRE.FindStringSubmatch(string(data))
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// EnsureGoModFromSpec writes go.mod from SPEC.md when the active phase includes go.mod and validation fails.
func EnsureGoModFromSpec(townRoot, rig string, v WorkflowValidation) (string, error) {
	if townRoot == "" || rig == "" || !WorkflowUsesGo(v) {
		return "", nil
	}
	scoped := v.ForActivePhase()
	if !activePhaseIncludesGoMod(scoped) {
		return "", nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var parts []string
	if ValidateGoModFile(rigDir, scoped) != nil {
		block := GoModBlockFromSpec(rigDir)
		if block == "" {
			return "", nil
		}
		layout := strings.Trim(strings.TrimSpace(scoped.LayoutRoot), "/")
		if layout == "" {
			layout = "."
		}
		modPath := filepath.Join(rigDir, layout, "go.mod")
		if err := os.MkdirAll(filepath.Dir(modPath), 0755); err != nil {
			return "", err
		}
		if err := os.WriteFile(modPath, []byte(block+"\n"), 0644); err != nil {
			return "", err
		}
		parts = append(parts, "patched "+filepath.ToSlash(filepath.Join(layout, "go.mod"))+" from SPEC.md Module block")
	}
	if ValidateGoModFile(rigDir, scoped) == nil {
		if closed, err := CloseGreenGoModBeads(townRoot, rig, scoped, nil); err != nil {
			return joinStrings(parts, "; "), err
		} else if len(closed) > 0 {
			parts = append(parts, "auto-closed go.mod bead(s): "+joinStrings(closed, ", "))
		}
	}
	return joinStrings(parts, "; "), nil
}

func activePhaseIncludesGoMod(v WorkflowValidation) bool {
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if strings.HasSuffix(f, "/go.mod") || f == "go.mod" {
			return true
		}
	}
	return false
}

// ResolveImplementBeadForVerify returns bead id/path for verify when activeBead may be unset.
func ResolveImplementBeadForVerify(townRoot, rig, activeBeadID string, v WorkflowValidation) (id, path string) {
	activeBeadID = strings.TrimSpace(activeBeadID)
	if activeBeadID != "" {
		return activeBeadID, ImplementBeadPathForID(townRoot, rig, activeBeadID, v)
	}
	next, err := NextOpenImplementBead(townRoot, rig, v)
	if err != nil || next == nil || next.ID == "" {
		return "", ""
	}
	return next.ID, ImplementBeadPathForID(townRoot, rig, next.ID, v)
}

// FormatGoModBeadContext returns prompt text for the go.mod implement bead.
func FormatGoModBeadContext(rigDir string, v WorkflowValidation) string {
	if !WorkflowUsesGo(v) {
		return ""
	}
	var b strings.Builder
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	modRel := "go.mod"
	if layout != "" && layout != "." {
		modRel = layout + "/go.mod"
	}
	b.WriteString("### go.mod bead — match SPEC.md exactly\n")
	b.WriteString("Use **WRITE:** `" + modRel + "` with the full Module block from SPEC (not EDIT/search-replace). Example:\n```\n")
	if block := GoModBlockFromSpec(rigDir); block != "" {
		b.WriteString(block)
	} else if reqs := RequiredGoModRequireDirectives(rigDir); len(reqs) > 0 {
		if canonical := canonicalGoModule(readFileString(filepath.Join(rigDir, "SPEC.md")), v); canonical != "" {
			b.WriteString("module " + canonical + "\n\n")
		}
		for _, req := range reqs {
			b.WriteString(req)
			b.WriteString("\n")
		}
	}
	b.WriteString("```\n")
	canonical := canonicalGoModule(readFileString(filepath.Join(rigDir, "SPEC.md")), v)
	if canonical != "" {
		b.WriteString("Module name: `")
		b.WriteString(canonical)
		b.WriteString("`. Verify: `")
		b.WriteString(GoModBeadVerifyCommand(v, rigDir))
		b.WriteString("` then `bd close`.\n")
	}
	b.WriteString("Do not add source files under paths not listed in architecture.md / required_files.\n")
	return strings.TrimSpace(b.String())
}

func readFileString(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}
