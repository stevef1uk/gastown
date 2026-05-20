package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
)

// DeliveryPhase is one delivery wave within a large rig spec (subset of required_files).
type DeliveryPhase struct {
	ID              string   `yaml:"id" json:"id"`
	Title           string   `yaml:"title,omitempty" json:"title,omitempty"`
	RequiredFiles   []string `yaml:"required_files" json:"required_files"`
	QAVerifyCommand string   `yaml:"qa_verify_command,omitempty" json:"qa_verify_command,omitempty"`
	DependsOn       []string `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
	SpecFocus       string   `yaml:"spec_focus,omitempty" json:"spec_focus,omitempty"`
}

// HasPhasedDelivery reports whether the profile defines delivery phases.
func (v WorkflowValidation) HasPhasedDelivery() bool {
	return len(v.DeliveryPhases) > 0
}

// ActivePhaseID returns the current phase id (trimmed).
func (v WorkflowValidation) ActivePhaseID() string {
	return strings.TrimSpace(v.ActivePhaseIDField)
}

// ActivePhase returns the delivery phase matching ActivePhaseID, or the first phase when unset.
func (v WorkflowValidation) ActivePhase() (DeliveryPhase, bool) {
	if !v.HasPhasedDelivery() {
		return DeliveryPhase{}, false
	}
	want := v.ActivePhaseID()
	if want != "" {
		for _, p := range v.DeliveryPhases {
			if strings.TrimSpace(p.ID) == want {
				return p, true
			}
		}
		return DeliveryPhase{}, false
	}
	p := v.DeliveryPhases[0]
	return p, true
}

// ActiveRequiredFiles returns paths in scope for the current delivery phase.
// When no phases are defined, returns RequiredFiles.
func (v WorkflowValidation) ActiveRequiredFiles() []string {
	if p, ok := v.ActivePhase(); ok && len(p.RequiredFiles) > 0 {
		return normalizePathList(p.RequiredFiles)
	}
	return normalizePathList(v.RequiredFiles)
}

// ActivePhaseQAVerifyCommand returns phase-specific QA command when set, else profile default.
func (v WorkflowValidation) ActivePhaseQAVerifyCommand() string {
	if p, ok := v.ActivePhase(); ok {
		if q := strings.TrimSpace(p.QAVerifyCommand); q != "" {
			return q
		}
	}
	return strings.TrimSpace(v.QAVerifyCommand)
}

// UnionRequiredFiles returns all paths across delivery phases (deduped), or RequiredFiles when unphased.
// Safe to call on ForActivePhase-scoped validation: phases still hold the full union.
func (v WorkflowValidation) UnionRequiredFiles() []string {
	if len(v.DeliveryPhases) == 0 {
		return normalizePathList(v.RequiredFiles)
	}
	seen := make(map[string]bool)
	var union []string
	add := func(paths []string) {
		for _, f := range normalizePathList(paths) {
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			union = append(union, f)
		}
	}
	for _, p := range v.DeliveryPhases {
		add(p.RequiredFiles)
	}
	add(v.RequiredFiles)
	return union
}

// ForActivePhase returns a copy of v with RequiredFiles and QAVerifyCommand scoped to the active phase.
func (v WorkflowValidation) ForActivePhase() WorkflowValidation {
	if !v.HasPhasedDelivery() {
		return v
	}
	out := v
	if files := v.ActiveRequiredFiles(); len(files) > 0 {
		out.RequiredFiles = append([]string(nil), files...)
	}
	if q := v.ActivePhaseQAVerifyCommand(); q != "" {
		out.QAVerifyCommand = q
	}
	return out
}

// IsDockerPackagingPath reports container packaging files (Dockerfile, compose, .dockerignore).
// These belong in the final delivery phase after application source exists.
func IsDockerPackagingPath(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	if path == "" {
		return false
	}
	lower := strings.ToLower(path)
	base := strings.ToLower(filepath.Base(path))
	if base == "dockerfile" || base == "containerfile" || base == ".dockerignore" {
		return true
	}
	return strings.Contains(lower, "docker-compose")
}

func sortDockerPackagingPaths(paths []string) []string {
	score := func(p string) int {
		lower := strings.ToLower(p)
		switch {
		case strings.HasSuffix(lower, "dockerfile") || strings.HasSuffix(lower, "containerfile"):
			return 0
		case strings.Contains(lower, "docker-compose") && strings.Contains(lower, ".test."):
			return 2
		case strings.Contains(lower, "docker-compose"):
			return 1
		default:
			return 3
		}
	}
	out := append([]string(nil), paths...)
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if score(out[j]) < score(out[i]) {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// moveDockerPathsToFinalDeliveryPhase pulls Dockerfile/compose paths out of early phases
// and appends them to the last phase so polecats implement real code before container wiring.
func moveDockerPathsToFinalDeliveryPhase(v WorkflowValidation) WorkflowValidation {
	if len(v.DeliveryPhases) == 0 {
		return v
	}
	seen := make(map[string]bool)
	var dockerPaths []string
	for i := range v.DeliveryPhases {
		var kept []string
		for _, f := range v.DeliveryPhases[i].RequiredFiles {
			if IsDockerPackagingPath(f) {
				if !seen[f] {
					seen[f] = true
					dockerPaths = append(dockerPaths, f)
				}
				continue
			}
			kept = append(kept, f)
		}
		v.DeliveryPhases[i].RequiredFiles = kept
	}
	if len(dockerPaths) == 0 {
		return v
	}
	dockerPaths = sortDockerPackagingPaths(dockerPaths)

	last := len(v.DeliveryPhases) - 1
	v.DeliveryPhases[last].RequiredFiles = append(
		normalizePathList(v.DeliveryPhases[last].RequiredFiles),
		dockerPaths...,
	)

	active := v.ActivePhaseID()
	var phases []DeliveryPhase
	for _, p := range v.DeliveryPhases {
		if len(p.RequiredFiles) == 0 {
			if active != "" && strings.TrimSpace(p.ID) == active {
				v.ActivePhaseIDField = ""
			}
			continue
		}
		phases = append(phases, p)
	}
	if len(phases) == 0 {
		return v
	}
	v.DeliveryPhases = phases
	return v
}

// FinalizeDeliveryPhases unions phase file lists into RequiredFiles, sets default active phase, normalizes paths.
func FinalizeDeliveryPhases(v WorkflowValidation) WorkflowValidation {
	if len(v.DeliveryPhases) == 0 {
		return v
	}
	v = moveDockerPathsToFinalDeliveryPhase(v)
	seen := make(map[string]bool)
	var union []string
	add := func(paths []string) {
		for _, f := range normalizePathList(paths) {
			if f == "" || seen[f] {
				continue
			}
			seen[f] = true
			union = append(union, f)
		}
	}
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].RequiredFiles = normalizePathList(v.DeliveryPhases[i].RequiredFiles)
		add(v.DeliveryPhases[i].RequiredFiles)
	}
	add(v.RequiredFiles)
	if len(union) > 0 {
		v.RequiredFiles = union
	}
	if v.ActivePhaseID() == "" && len(v.DeliveryPhases) > 0 {
		v.ActivePhaseIDField = strings.TrimSpace(v.DeliveryPhases[0].ID)
	}
	return v
}

func normalizePathList(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// NormalizeDeliveryPhasesLayout prefixes phase required_files with layout_root like RequiredFiles.
func NormalizeDeliveryPhasesLayout(v WorkflowValidation) WorkflowValidation {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" || layout == "." {
		return v
	}
	for i := range v.DeliveryPhases {
		if len(v.DeliveryPhases[i].RequiredFiles) == 0 {
			continue
		}
		out := make([]string, 0, len(v.DeliveryPhases[i].RequiredFiles))
		for _, f := range v.DeliveryPhases[i].RequiredFiles {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if f == "" {
				continue
			}
			if !strings.HasPrefix(f, layout+"/") && !strings.Contains(f, "..") {
				f = layout + "/" + strings.TrimPrefix(f, "/")
			}
			out = append(out, f)
		}
		v.DeliveryPhases[i].RequiredFiles = out
	}
	return v
}

// PhaseScopeNote returns planner/polecat guidance when delivery phases are configured.
func (v WorkflowValidation) PhaseScopeNote() string {
	if !v.HasPhasedDelivery() {
		return ""
	}
	activeID := v.ActivePhaseID()
	if p, ok := v.ActivePhase(); ok && activeID == "" {
		activeID = strings.TrimSpace(p.ID)
	}
	title := ""
	if p, ok := v.ActivePhase(); ok {
		title = strings.TrimSpace(p.Title)
	}
	line := "**Phased delivery:** active phase `" + activeID + "`"
	if title != "" {
		line += " (" + title + ")"
	}
	activeFiles := v.ActiveRequiredFiles()
	line += fmt.Sprintf(". Create implement beads **only** for the %d paths in `required_files` below", len(activeFiles))
	line += " — **not** every path in `architecture.md` (later phases get their own beads). "
	line += "`plan.md` only needs to cover this phase (" + v.PlanMinSizeHint() + "). "
	line += "Read architecture for context; do **not** `bd create` for backend/frontend/test paths until their phase is active."
	return line
}

// PhaseSummaryLines returns human-readable phase list for operator notices.
func (v WorkflowValidation) PhaseSummaryLines() []string {
	if !v.HasPhasedDelivery() {
		return nil
	}
	active := v.ActivePhaseID()
	var lines []string
	for _, p := range v.DeliveryPhases {
		id := strings.TrimSpace(p.ID)
		title := strings.TrimSpace(p.Title)
		label := id
		if title != "" {
			label = id + " — " + title
		}
		n := len(p.RequiredFiles)
		mark := ""
		if active != "" && id == active {
			mark = " (active)"
		} else if active == "" && len(lines) == 0 {
			mark = " (default active)"
		}
		lines = append(lines, fmt.Sprintf("%s: %d file(s)%s", label, n, mark))
	}
	return lines
}
