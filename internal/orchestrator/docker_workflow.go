package orchestrator

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

var (
	dockerComposeCLICache     string
	dockerComposeCLICacheOnce sync.Once
	dockerComposeCLIOverride  string // non-empty in tests only
)

// DockerComposeCLI returns the host's compose invocation: "docker compose" (plugin) or "docker-compose" (standalone).
func DockerComposeCLI() string {
	if dockerComposeCLIOverride != "" {
		return dockerComposeCLIOverride
	}
	dockerComposeCLICacheOnce.Do(func() {
		dockerComposeCLICache = detectDockerComposeCLI()
	})
	return dockerComposeCLICache
}

func detectDockerComposeCLI() string {
	if path, err := exec.LookPath("docker"); err == nil {
		cmd := exec.Command(path, "compose", "version")
		if err := cmd.Run(); err == nil {
			return "docker compose"
		}
	}
	if path, err := exec.LookPath("docker-compose"); err == nil {
		cmd := exec.Command(path, "version")
		if err := cmd.Run(); err == nil {
			return "docker-compose"
		}
	}
	return "docker compose"
}

// AdaptDockerComposeCommand rewrites compose CLI spelling to match DockerComposeCLI.
// Only command tokens are changed (trailing space); paths like docker-compose.yml are left alone.
func AdaptDockerComposeCommand(cmd string) string {
	if cmd == "" {
		return cmd
	}
	want := DockerComposeCLI()
	if want == "docker-compose" {
		return replaceComposeInvocation(cmd, "docker compose", "docker-compose")
	}
	return replaceComposeInvocation(cmd, "docker-compose", "docker compose")
}

func replaceComposeInvocation(cmd, from, to string) string {
	withSpace := from + " "
	if strings.Contains(cmd, withSpace) {
		cmd = strings.ReplaceAll(cmd, withSpace, to+" ")
	}
	if strings.HasSuffix(cmd, from) {
		cmd = cmd[:len(cmd)-len(from)] + to
	}
	return cmd
}

// WorkflowUsesDocker reports Docker-based verify (custom runner + docker in QA command or Dockerfile paths).
func WorkflowUsesDocker(v WorkflowValidation) bool {
	if WorkflowUsesGo(v) || WorkflowUsesPython(v) {
		return false
	}
	qa := strings.ToLower(strings.TrimSpace(v.QAVerifyCommand))
	if strings.Contains(qa, "docker") {
		return true
	}
	for _, f := range v.UnionRequiredFiles() {
		f = strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		if strings.HasSuffix(f, "dockerfile") || strings.Contains(f, "docker-compose") {
			return true
		}
	}
	return false
}

// NormalizeDockerCommand fixes common model typos in docker CLI invocations.
func NormalizeDockerCommand(cmd string) string {
	repls := []struct{ old, new string }{
		{"docker build.", "docker build ."},
		{"docker build..", "docker build ."},
		{"docker compose.", "docker compose"},
		{"build:.", "build: ."},
	}
	out := cmd
	for _, r := range repls {
		if strings.Contains(out, r.old) {
			out = strings.ReplaceAll(out, r.old, r.new)
		}
	}
	return AdaptDockerComposeCommand(out)
}

// DockerImplementationVerifyCommandForBead returns verify scoped to layout_root and bead path.
func DockerImplementationVerifyCommandForBead(v WorkflowValidation, mayorRigDir, beadPath string) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	base := "cd " + layout

	switch {
	case strings.HasSuffix(strings.ToLower(beadPath), "dockerfile"):
		return base + " && docker build -f Dockerfile ."
	case strings.Contains(strings.ToLower(beadPath), "docker-compose"):
		return base + " && " + DockerComposeCLI() + " -f docker-compose.yml config"
	case strings.HasSuffix(beadPath, ".env.example"), strings.HasSuffix(beadPath, ".env"):
		return "test -f " + beadPath
	default:
		if q := strings.TrimSpace(v.ActivePhaseQAVerifyCommand()); q != "" {
			return dockerVerifyWithLayout(q, layout)
		}
		return dockerVerifyWithLayout(strings.TrimSpace(v.QAVerifyCommand), layout)
	}
}

func effectiveLayoutRoot(layout string) string {
	layout = strings.Trim(filepath.ToSlash(strings.TrimSpace(layout)), "/")
	if layout == "" || layout == "." {
		return "."
	}
	return layout
}

// stripBrokenCdPrefix removes LLM output like "cd && docker …" when no path was given.
func stripBrokenCdPrefix(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	for {
		lower := strings.ToLower(cmd)
		switch {
		case strings.HasPrefix(lower, "cd &&"):
			cmd = strings.TrimSpace(cmd[5:])
		case strings.HasPrefix(lower, "cd  &&"):
			cmd = strings.TrimSpace(cmd[6:])
		default:
			return cmd
		}
	}
}

func dockerVerifyWithLayout(cmd, layout string) string {
	cmd = stripBrokenCdPrefix(NormalizeDockerCommand(strings.TrimSpace(cmd)))
	layout = effectiveLayoutRoot(layout)
	if cmd == "" {
		if layout == "." {
			return ""
		}
		return "cd " + layout
	}
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "cd ") {
		return cmd
	}
	// Flat repo (mayor/rig root): agents already cwd there — do not emit "cd . &&".
	if layout == "." {
		return cmd
	}
	return "cd " + layout + " && " + cmd
}

// DoubledLayoutPath reports paths like finally/finally/Dockerfile when layout_root is finally.
// isRigFlowNonImplementPath reports paths spec-index sometimes emits that rig-flow does not implement via polecat beads.
func isRigFlowNonImplementPath(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(path)))
	return strings.HasSuffix(lower, "/planning/plan.md") || lower == "planning/plan.md"
}

func fixDoubledLayoutPath(path, layoutRoot string) string {
	layoutRoot = strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	if layoutRoot == "" {
		return path
	}
	doubled := layoutRoot + "/" + layoutRoot + "/"
	return strings.Replace(filepath.ToSlash(path), doubled, layoutRoot+"/", 1)
}

func filterRigFlowRequiredFiles(files []string, layoutRoot string) []string {
	var out []string
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" || isRigFlowNonImplementPath(f) {
			continue
		}
		if DoubledLayoutPath(f, layoutRoot) {
			f = fixDoubledLayoutPath(f, layoutRoot)
		}
		out = append(out, f)
	}
	return out
}

// SanitizeRigFlowProfile fixes spec-index / hand-edited profiles for rig-flow execution.
func SanitizeRigFlowProfile(v WorkflowValidation) WorkflowValidation {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" {
		v.LayoutRoot = "."
		layout = "."
	}
	v.RequiredFiles = filterRigFlowRequiredFiles(v.RequiredFiles, layout)
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].RequiredFiles = filterRigFlowRequiredFiles(v.DeliveryPhases[i].RequiredFiles, layout)
		if q := strings.TrimSpace(v.DeliveryPhases[i].QAVerifyCommand); q != "" && WorkflowUsesDocker(v) {
			v.DeliveryPhases[i].QAVerifyCommand = dockerVerifyWithLayout(q, layout)
		}
	}
	if WorkflowUsesDocker(v) {
		if q := strings.TrimSpace(v.QAVerifyCommand); q != "" {
			v.QAVerifyCommand = dockerVerifyWithLayout(q, layout)
		}
	}
	switch strings.TrimSpace(v.BeadTitleContains) {
	case "", "Implement":
		if layout != "" && layout != "." {
			v.BeadTitleContains = "Implement " + layout + "/"
		} else {
			v.BeadTitleContains = "Implement "
		}
	case "Implement finally/":
		// spec-index often confuses rig name with layout_root on flat mayor/rig worktrees
		if layout == "." {
			v.BeadTitleContains = "Implement "
		}
	}
	return v
}

func stripLayoutPrefixFromPaths(files []string, layout string) []string {
	layout = strings.Trim(filepath.ToSlash(strings.TrimSpace(layout)), "/")
	if layout == "" || layout == "." {
		return files
	}
	prefix := layout + "/"
	var out []string
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		f = fixDoubledLayoutPath(f, layout)
		if strings.HasPrefix(f, prefix) {
			f = strings.TrimPrefix(f, prefix)
		}
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// AlignProfileLayoutWithArchitecture fixes spec-index confusing rig name with layout_root.
// When architecture.md lists paths like backend/main.py but the profile uses finally/backend/…,
// strip the rig prefix and set layout_root to "." (mayor/rig is the repo root).
func AlignProfileLayoutWithArchitecture(v WorkflowValidation, archPath string) WorkflowValidation {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" || layout == "." {
		return v
	}
	data, err := os.ReadFile(archPath)
	if err != nil || len(data) == 0 {
		return v
	}
	archPaths := extractArchPaths(string(data), "")
	if len(archPaths) < 3 {
		return v
	}
	prefixed := 0
	for _, p := range archPaths {
		if strings.HasPrefix(p, layout+"/") {
			prefixed++
		}
	}
	if prefixed > 0 {
		return v
	}
	profileUsesLayout := false
	for _, f := range v.UnionRequiredFiles() {
		if strings.HasPrefix(filepath.ToSlash(f), layout+"/") {
			profileUsesLayout = true
			break
		}
	}
	if !profileUsesLayout {
		return v
	}
	v.LayoutRoot = "."
	v.BeadTitleContains = "Implement "
	v.RequiredFiles = stripLayoutPrefixFromPaths(v.RequiredFiles, layout)
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].RequiredFiles = stripLayoutPrefixFromPaths(v.DeliveryPhases[i].RequiredFiles, layout)
		if q := strings.TrimSpace(v.DeliveryPhases[i].QAVerifyCommand); q != "" {
			v.DeliveryPhases[i].QAVerifyCommand = dockerVerifyWithLayout(q, ".")
		}
	}
	if q := strings.TrimSpace(v.QAVerifyCommand); q != "" {
		v.QAVerifyCommand = dockerVerifyWithLayout(q, ".")
	}
	return v
}

func DoubledLayoutPath(path, layoutRoot string) bool {
	layoutRoot = strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	path = filepath.ToSlash(strings.TrimSpace(path))
	if layoutRoot == "" || path == "" {
		return false
	}
	return strings.Contains(path, layoutRoot+"/"+layoutRoot+"/")
}
