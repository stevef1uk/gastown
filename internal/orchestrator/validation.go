package orchestrator

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// WorkflowValidation configures artifact checks for a workflow template (rig-flow, etc.).
// Operators edit these fields in orchestrator/templates/*.yaml when changing SPEC scope.
type WorkflowValidation struct {
	LayoutRoot                 string          `yaml:"layout_root" json:"layout_root"`
	BeadTitleContains          string          `yaml:"bead_title_contains" json:"bead_title_contains"`
	UnittestModule             string          `yaml:"unittest_module" json:"unittest_module"`
	QAVerifyCommand            string          `yaml:"qa_verify_command" json:"qa_verify_command"`
	TestRunner                 string          `yaml:"test_runner" json:"test_runner"`
	RequiredFiles              []string        `yaml:"required_files" json:"required_files"`
	DeliveryPhases             []DeliveryPhase `yaml:"delivery_phases" json:"delivery_phases,omitempty"`
	ActivePhaseIDField         string          `yaml:"active_phase_id" json:"active_phase_id,omitempty"`
	RewoundFromPhaseIDField    string          `yaml:"rewound_from_phase_id,omitempty" json:"rewound_from_phase_id,omitempty"`
	CompletedPhaseIDsField     []string        `yaml:"completed_phase_ids,omitempty" json:"completed_phase_ids,omitempty"`
	SpecSummary                string          `yaml:"spec_summary" json:"spec_summary"`
	MinArchitectureBytes       int64           `yaml:"min_architecture_bytes" json:"min_architecture_bytes"`
	MinPlanBytes               int64           `yaml:"min_plan_bytes" json:"min_plan_bytes"`
	MinImplementationFileBytes int64           `yaml:"min_implementation_file_bytes" json:"min_implementation_file_bytes"`
	MinSubstantiveLines        int             `yaml:"min_substantive_lines" json:"min_substantive_lines"`
	// MinTestPlanBytes is the minimum size of TEST_PLAN.md (default 400). The tester
	// reviews test coverage against SPEC.md/architecture.md; a plan below this floor
	// is treated as a stub and bounces back to the tester before any tests run.
	MinTestPlanBytes int64 `yaml:"min_test_plan_bytes" json:"min_test_plan_bytes"`
	// UICommand is the optional command that starts the project's UI (e.g. "npm run dev").
	// When set, the tester requires a UI test per UI-facing requirement at test_review.
	UICommand string `yaml:"ui_command" json:"ui_command"`
	// MaxReviewRetries caps how many consecutive test_review failures the tester may
	// incur on the same TEST_PLAN.md row before the workflow is forced to plan_gap
	// (back to test_plan) or architecture_failure (back to design). Guards against
	// a polecat that can't make a flaky test green looping forever. Default 3.
	MaxReviewRetries int `yaml:"max_review_retries" json:"max_review_retries"`
	// PythonVenvDir is the venv directory under mayor/rig (default ".venv"). Set "off" to disable.
	PythonVenvDir string `yaml:"python_venv_dir" json:"python_venv_dir"`
	// DevServerPort is the port the dev server listens on when the project is a web server.
	// 0 means the project is not a server (CLI, library) — no port cleanup needed.
	// Set during spec-index from SPEC.md; used by StopDevServersForRig.
	DevServerPort int `yaml:"dev_server_port" json:"dev_server_port"`
}

// Artifact size guard defaults for rig-flow (gt rig spec-index / workflow-profile.json).
// LLMs often emit min_plan_bytes near the full SPEC size; ClampProfileValidation corrects that.
const (
	MinArtifactBytesFloor       int64 = 200
	DefaultMinPlanBytes         int64 = 2500
	MaxMinPlanBytes             int64 = 4096
	DefaultMinArchitectureBytes int64 = 4000
	MaxMinArchitectureBytes     int64 = 8192
	// DefaultMinTestPlanBytes is the default minimum TEST_PLAN.md size (400 bytes).
	DefaultMinTestPlanBytes int64 = 400
	// MaxMinTestPlanBytes caps min_test_plan_bytes from spec-index LLM output.
	MaxMinTestPlanBytes int64 = 2048
	// DefaultMaxReviewRetries is the default tester.max_review_retries (3).
	DefaultMaxReviewRetries = 3
	// SmallRigMaxArchitectureBytes caps min_architecture_bytes when the profile lists few files
	// (e.g. Link Shelf). Spec-index often requests 8k+; a complete doc for 7 paths is ~3–4k.
	SmallRigMaxArchitectureBytes int64 = 3200
	smallRigRequiredFileCap            = 10
)

// DefaultWorkflowValidation returns minimal rig-flow defaults when YAML/profile omit validation.
// Per-rig values should come from mayor/rig/.gastown/workflow-profile.json (gt rig spec-index).
func DefaultWorkflowValidation() WorkflowValidation {
	return WorkflowValidation{
		BeadTitleContains:    "Implement ",
		MinArchitectureBytes: MinArtifactBytesFloor,
		MinPlanBytes:         MinArtifactBytesFloor,
		MinTestPlanBytes:     DefaultMinTestPlanBytes,
		MaxReviewRetries:     DefaultMaxReviewRetries,
	}
}

// MinPlanBytesFromArchitecture returns the minimum plan.md size: quarter of architecture bytes (floored/capped).
func MinPlanBytesFromArchitecture(architectureBytes int64) int64 {
	if architectureBytes < 0 {
		architectureBytes = 0
	}
	n := architectureBytes / 4
	if n < MinArtifactBytesFloor {
		n = MinArtifactBytesFloor
	}
	if n > MaxMinPlanBytes {
		n = MaxMinPlanBytes
	}
	return n
}

// phasedPlanByteScale returns the fraction of architecture.md that applies to plan.md sizing.
// With delivery_phases, planning covers only ActiveRequiredFiles — not the full union.
func (v WorkflowValidation) phasedPlanByteScale() float64 {
	if !v.HasPhasedDelivery() {
		return 1
	}
	total := len(v.UnionRequiredFiles())
	active := len(v.ActiveRequiredFiles())
	if total == 0 || active == 0 || active >= total {
		return 1
	}
	ratio := float64(active) / float64(total)
	const minRatio = 0.12 // avoid tiny plans when a phase has only a few paths
	if ratio < minRatio {
		ratio = minRatio
	}
	return ratio
}

// EffectiveMinPlanBytes returns the plan.md minimum for a rig: quarter of on-disk architecture.md when present
// (scaled by active delivery phase when phased), otherwise quarter of min_architecture_bytes from the profile.
func EffectiveMinPlanBytes(rigDir string, v WorkflowValidation) int64 {
	var archBytes int64
	archPath := filepath.Join(rigDir, "architecture.md")
	if info, err := os.Stat(archPath); err == nil && info.Size() > 0 {
		archBytes = info.Size()
	} else if v.MinArchitectureBytes > 0 {
		archBytes = v.MinArchitectureBytes
	} else {
		return MinPlanBytesFromArchitecture(0)
	}
	scaled := int64(float64(archBytes) * v.phasedPlanByteScale())
	return MinPlanBytesFromArchitecture(scaled)
}

// PlanMinSizeHint describes how EffectiveMinPlanBytes was derived (for errors and prompts).
func (v WorkflowValidation) PlanMinSizeHint() string {
	if v.HasPhasedDelivery() && len(v.ActiveRequiredFiles()) < len(v.UnionRequiredFiles()) {
		id := v.ActivePhaseID()
		if id == "" {
			if p, ok := v.ActivePhase(); ok {
				id = strings.TrimSpace(p.ID)
			}
		}
		if id != "" {
			return fmt.Sprintf("quarter of architecture.md scaled for delivery phase %q", id)
		}
		return "quarter of architecture.md scaled for active delivery phase"
	}
	return "quarter of architecture.md"
}

// ClampProfileValidation normalizes min_*_bytes from spec-index LLM output or hand-edited profiles.
func ClampProfileValidation(v WorkflowValidation) WorkflowValidation {
	v.MinArchitectureBytes = clampArtifactBytes(v.MinArchitectureBytes, DefaultMinArchitectureBytes, MinArtifactBytesFloor, MaxMinArchitectureBytes)
	v.MinImplementationFileBytes = clampArtifactBytes(
		v.MinImplementationFileBytes, DefaultMinImplementationFileBytes, MinImplementationFileBytesFloor, MaxMinImplementationFileBytes,
	)
	if v.MinSubstantiveLines < 1 {
		v.MinSubstantiveLines = DefaultMinSubstantiveLines
	}
	if v.MinSubstantiveLines > 20 {
		v.MinSubstantiveLines = DefaultMinSubstantiveLines
	}
	v = capArchitectureBytesForSmallRig(v)
	minPlan := MinPlanBytesFromArchitecture(v.MinArchitectureBytes)
	if v.MinPlanBytes < MinArtifactBytesFloor || v.MinPlanBytes > minPlan {
		v.MinPlanBytes = minPlan
	}
	v.MinTestPlanBytes = clampArtifactBytes(v.MinTestPlanBytes, DefaultMinTestPlanBytes, MinArtifactBytesFloor, MaxMinTestPlanBytes)
	if v.MaxReviewRetries < 1 || v.MaxReviewRetries > 10 {
		v.MaxReviewRetries = DefaultMaxReviewRetries
	}
	v = FinalizeDeliveryPhases(v)
	v = StripInvalidCDPrefixes(v)
	v = validatePhaseVerifyCommands(v)
	v = validatePhaseVerifyCommandsAgainstFiles(v)
	v.RequiredFiles = StripNonFileRequiredEntries(v.RequiredFiles)
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].RequiredFiles = StripNonFileRequiredEntries(v.DeliveryPhases[i].RequiredFiles)
	}
	v.RequiredFiles = deduplicateRequiredFiles(v.RequiredFiles)
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].RequiredFiles = deduplicateRequiredFiles(v.DeliveryPhases[i].RequiredFiles)
	}
	v = InjectSQLiteSchemaBead(v)
	v = SanitizeRigFlowProfile(v)
	v = ValidateDeliveryPhases(v)
	v = stripRuntimeSmokeFromPhaseCommands(v)
	// Deterministic post-judge guard must also run on every load path (task
	// validation, rig sync-planning, rig setup) — not just spec-index. Without
	// this, the JUDGE's rewrite of integration-test to "go test ./..." survives
	// any later profile write, and QA never runs the Playwright compose service.
	// Runs LAST so no subsequent transformation can clobber the restored command.
	v = SanitizePhaseVerifyCommandsForStack(v)
	// Runs LAST: docker compose E2E verify commands must rebuild with --no-cache so
	// QA never verifies a stale tagged image. No later transformation may strip it.
	v.QAVerifyCommand = NormalizeDockerQACommand(v.QAVerifyCommand)
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].QAVerifyCommand = NormalizeDockerQACommand(v.DeliveryPhases[i].QAVerifyCommand)
	}
	return v
}

// stripRuntimeSmokeFromPhaseCommands removes go run/curl smoke commands from
// phase qa_verify_command when dev_server_port is 0 (smoke disabled). The JUDGE
// LLM sometimes generates go run + curl smoke tests even when port=0. The
// replacement is stack-appropriate (never go vet in a Python phase).
func stripRuntimeSmokeFromPhaseCommands(v WorkflowValidation) WorkflowValidation {
	if v.DevServerPort > 0 {
		return v
	}
	for i := range v.DeliveryPhases {
		cmd := strings.TrimSpace(v.DeliveryPhases[i].QAVerifyCommand)
		if cmd == "" {
			continue
		}
		lower := strings.ToLower(cmd)
		if strings.Contains(lower, "go run") || strings.Contains(lower, "curl ") {
			p := &v.DeliveryPhases[i]
			v.DeliveryPhases[i].QAVerifyCommand = defaultQAVerifyForPhase(p, v.LayoutRoot)
			log.Printf("[clamp] phase %q: stripped runtime-smoke cmd (dev_server_port=0), replaced with stack verify %q", v.DeliveryPhases[i].ID, v.DeliveryPhases[i].QAVerifyCommand)
		}
	}
	return v
}

// SanitizePhaseVerifyCommandsForStack repairs phase verify commands that don't
// match the stack of the phase's required files, across Go, Python, and Node.
// This is the deterministic guard against JUDGE LLM hallucinations (e.g. "go vet
// ./..." in a Python phase, "npm test" in a Go phase) that run after the judge
// step, which skips ClampProfileValidation.
func SanitizePhaseVerifyCommandsForStack(v WorkflowValidation) WorkflowValidation {
	if len(v.DeliveryPhases) == 0 {
		return v
	}
	for i := range v.DeliveryPhases {
		p := &v.DeliveryPhases[i]
		cmd := strings.TrimSpace(p.QAVerifyCommand)
		lower := strings.ToLower(cmd)
		hasGo, hasPy, hasNode := phaseFileStacks(p.RequiredFiles)
		stackCount := boolCount(hasGo) + boolCount(hasPy) + boolCount(hasNode)
		bad := false
		if cmd == "" {
			bad = true
		}
		if v.DevServerPort == 0 && (strings.Contains(lower, "go run") || strings.Contains(lower, "curl ")) {
			bad = true
		}
		// Cross-stack mismatch: the phase's files pin one stack, but the command
		// invokes tools of a different stack and none of its own (e.g. pure
		// "go vet" in a Python phase, "npm test" in a Go phase, "pytest" in a
		// Node phase). Commands that mix their own stack with others are kept.
		if stackCount == 1 {
			hasOwn := (hasGo && hasGoTool(lower)) || (hasPy && hasPythonTool(lower)) || (hasNode && hasNodeTool(lower))
			hasForeign := (!hasGo && hasGoTool(lower)) || (!hasPy && hasPythonTool(lower)) || (!hasNode && hasNodeTool(lower))
			if !hasOwn && hasForeign {
				bad = true
			}
		}
		// Config-only phases (requirements.txt, Dockerfile, etc.) must not run
		// any stack tool — "go vet" on a requirements-only phase fails at runtime.
		if stackCount == 0 && hasAnyStackTool(lower) {
			bad = true
		}
		if bad {
			p.QAVerifyCommand = defaultQAVerifyForPhase(p, v.LayoutRoot)
		}
		// Deterministic guard for integration-test / e2e phases: when the phase
		// ships a docker-compose file AND playwright scaffolding, the verify must
		// run the compose Playwright service. The JUDGE LLM repeatedly rewrites
		// these to "npx playwright test --list" (wrong path / no browser install);
		// clamp it back to the Docker command after the judge runs.
		if phaseShipsDockerPlaywright(p) {
			p.QAVerifyCommand = composePlaywrightVerifyCommand(p, v.LayoutRoot)
		}
	}
	return v
}

func boolCount(b bool) int {
	if b {
		return 1
	}
	return 0
}

var (
	goToolRE   = regexp.MustCompile(`(^|[\s;&|])go\s+(test|vet|build|run|mod)\b`)
	pyToolRE   = regexp.MustCompile(`\b(python3?|pytest|pip|uvicorn|gunicorn|flask)\b`)
	nodeToolRE = regexp.MustCompile(`\b(npm|npx|yarn|pnpm|node|tsc)\b`)
	allStackRE = regexp.MustCompile(`(^|[\s;&|])go\s+(test|vet|build|run|mod)\b|\b(python3?|pytest|pip|uvicorn|gunicorn|flask)\b|\b(npm|npx|yarn|pnpm|node|tsc)\b`)
)

func hasGoTool(cmd string) bool       { return goToolRE.MatchString(cmd) }
func hasPythonTool(cmd string) bool   { return pyToolRE.MatchString(cmd) }
func hasNodeTool(cmd string) bool     { return nodeToolRE.MatchString(cmd) }
func hasAnyStackTool(cmd string) bool { return allStackRE.MatchString(cmd) }

// pythonServerEntryModule derives the import module and working directory for a
// Python server entry point (main.py, app.py, server.py, or any .../api/ file).
// Returns (module, cwd) where cwd is the directory from which to run the import.
// For example:
//   layout=finally, file=finally/backend/app/main.py → ("app.main", "finally/backend")
//   layout=., file=app.py → ("app", ".")
func pythonServerEntryModule(files []string, layoutRoot string) (string, string) {
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		// Remove layout root prefix if present
		rel := f
		if layoutRoot != "" && layoutRoot != "." && strings.HasPrefix(f, layoutRoot+"/") {
			rel = strings.TrimPrefix(f, layoutRoot+"/")
		}
		lower := strings.ToLower(rel)
		// Detect server entry files: main.py, app.py, server.py, or any file under .../api/
		if strings.HasSuffix(lower, "/main.py") ||
			strings.HasSuffix(lower, "/app.py") ||
			strings.HasSuffix(lower, "/server.py") ||
			strings.Contains(lower, "/api/") {
			// Derive the module import path from the directory containing the file
			dir := filepath.Dir(rel)
			// Convert path separators to dots for Python import
			// e.g., backend/app/main.py → dir=backend/app → module=app.main
			parts := strings.Split(dir, "/")
			var modParts []string
			for _, part := range parts {
				if part != "" {
					modParts = append(modParts, part)
				}
			}
			if len(modParts) > 0 {
				// The module is the last two parts: e.g., backend/app → app.main
				// Actually we want the package + filename (without .py)
				fileName := strings.TrimSuffix(filepath.Base(f), ".py")
				if len(modParts) >= 1 {
					// Use the last directory as the package, plus the filename
					pkg := modParts[len(modParts)-1]
					module := pkg + "." + fileName
					// cwd is the parent of the package directory
					var cwdParts []string
					if len(modParts) > 1 {
						cwdParts = modParts[:len(modParts)-1]
					}
					cwd := layoutRoot
					if len(cwdParts) > 0 {
						cwd = filepath.Join(layoutRoot, filepath.Join(cwdParts...))
					}
					return module, cwd
				}
			}
		}
	}
	return "", ""
}

// phaseFileStacks reports which of Go/Python/Node the phase's required files imply.
func phaseFileStacks(files []string) (hasGo, hasPy, hasNode bool) {
	for _, f := range files {
		f = strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		if strings.HasSuffix(f, ".go") || strings.HasSuffix(f, "go.mod") {
			hasGo = true
		}
		if strings.HasSuffix(f, ".py") || strings.HasPrefix(f, "tests/") {
			hasPy = true
		}
		if strings.HasSuffix(f, ".ts") || strings.HasSuffix(f, ".tsx") ||
			strings.HasSuffix(f, ".js") || strings.HasSuffix(f, ".jsx") ||
			strings.HasSuffix(f, ".mjs") || strings.HasSuffix(f, ".cjs") ||
			strings.HasSuffix(f, "tsconfig.json") || strings.HasSuffix(f, "package.json") {
			hasNode = true
		}
	}
	return hasGo, hasPy, hasNode
}

// phaseShipsDockerPlaywright reports whether a delivery phase ships both a
// docker-compose file and Playwright scaffolding (config, spec, or package.json),
// i.e. the phase is meant to run E2E tests in the Playwright Docker container.
func phaseShipsDockerPlaywright(p *DeliveryPhase) bool {
	if p == nil {
		return false
	}
	// Content-driven, not name-driven: a phase that ships BOTH a docker-compose
	// file AND Playwright scaffolding (config/spec/package.json) is an E2E phase,
	// whatever its id/title says (e.g. FinAlly's "Testing & Release" phase). The
	// old isIntegration name gate silently skipped those phases, leaving the weak
	// `test -f docker-compose.yml && echo` command in place and Playwright never ran.
	hasCompose := false
	hasPlaywright := false
	for _, f := range p.RequiredFiles {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		if strings.Contains(lower, "docker-compose") {
			hasCompose = true
		}
		// Playwright scaffolding is not only a file literally named playwright.config.*:
		// e2e.spec.ts / *.spec.ts / test/e2e/ paths are E2E tests that require the
		// playwright compose runner. Without this, phases shipping only
		// test/e2e.spec.ts + docker-compose.test.yml escaped the clamp and kept the
		// weak `test -f docker-compose.yml && echo` verify (FinAlly regression).
		if strings.Contains(lower, "playwright") || IsE2ETestPath(f) {
			hasPlaywright = true
		}
	}
	return hasCompose && hasPlaywright
}

// composePlaywrightVerifyCommand returns the Docker-compose Playwright verify
// command for a phase, scoped to layout_root. It locates the docker-compose file
// in the phase's required files (so test/docker-compose.yml rigs work, not just
// layout-root compose files) and adapts the CLI spelling to the host
// (docker-compose standalone vs docker compose plugin).
//
// The command tears down any existing project containers first (docker-compose
// down) before bringing the stack up. Without this, a stale web container from
// a previous run can keep host ports bound and cause the next QA run to fail
// with "address already in use".
func composePlaywrightVerifyCommand(p *DeliveryPhase, layoutRoot string) string {
	lr := layoutRoot
	if lr == "" {
		lr = "."
	}
	composeFile := dockerComposeFileForPhase(p.RequiredFiles)
	cli := DockerComposeCLI()
	if composeFile != "" {
		rel := composeFile
		if lr != "." && strings.HasPrefix(composeFile, lr+"/") {
			rel = strings.TrimPrefix(composeFile, lr+"/")
		}
		return fmt.Sprintf("cd %s && %s -f %s down && %s -f %s up --exit-code-from playwright", lr, cli, rel, cli, rel)
	}
	return fmt.Sprintf("cd %s && %s down && %s up --exit-code-from playwright", lr, cli, cli)
}

// dockerComposeFileForPhase picks the docker-compose file a phase's Playwright
// E2E run should target. When a phase ships both a production compose file (e.g.
// docker-compose.yml) and a test harness (e.g. test/docker-compose.test.yml), the
// test harness is the one that defines the "playwright" service — the production
// compose has no such service and `--exit-code-from playwright` fails with
// "no such service: playwright". Files whose path contains a test/e2e/spec
// segment or basename are preferred; otherwise the first docker-compose file wins.
func dockerComposeFileForPhase(files []string) string {
	fallback := ""
	best := ""
	bestScore := -1
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		lower := strings.ToLower(f)
		if !strings.Contains(lower, "docker-compose") {
			continue
		}
		if fallback == "" {
			fallback = f
		}
		score := dockerComposeHarnessScore(lower)
		if score > bestScore {
			bestScore = score
			best = f
		}
	}
	if bestScore > 0 {
		return best
	}
	return fallback
}

// dockerComposeHarnessScore ranks a docker-compose file by how likely it is a
// test/E2E harness: higher scores mean more test-oriented.
func dockerComposeHarnessScore(lower string) int {
	score := 0
	if strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") {
		score += 3
	}
	if strings.Contains(lower, "/e2e/") || strings.Contains(lower, "/spec/") {
		score += 2
	}
	base := lower
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if strings.Contains(base, "test") {
		score += 2
	}
	if strings.Contains(base, "e2e") || strings.Contains(base, "spec") {
		score += 1
	}
	return score
}

// StripInvalidCDPrefixes removes leading "cd <dir> && " from verify commands when layout_root
// is empty or "." and <dir> is not a real subdirectory of the project. This catches LLM
// output that uses the rig name (e.g. "cd finally") instead of layout_root.
func StripInvalidCDPrefixes(v WorkflowValidation) WorkflowValidation {
	if v.LayoutRoot != "" && v.LayoutRoot != "." {
		return v
	}
	topDirs := make(map[string]bool)
	for _, f := range v.RequiredFiles {
		if idx := strings.IndexByte(f, '/'); idx > 0 {
			topDirs[f[:idx]] = true
		}
	}

	oldQA := v.QAVerifyCommand
	v.QAVerifyCommand = stripNestedBogusCD(stripBogusLeadCD(v.QAVerifyCommand, topDirs), topDirs)
	if v.QAVerifyCommand != oldQA {
		log.Printf("[strip-cd] top-level QA: %q → %q (topDirs=%v)", oldQA, v.QAVerifyCommand, topDirs)
	}
	for i := range v.DeliveryPhases {
		p := &v.DeliveryPhases[i]
		old := p.QAVerifyCommand
		p.QAVerifyCommand = stripNestedBogusCD(stripBogusLeadCD(p.QAVerifyCommand, topDirs), topDirs)
		if p.QAVerifyCommand != old {
			log.Printf("[strip-cd] phase %q: %q → %q", p.ID, old, p.QAVerifyCommand)
		}
	}
	return v
}

// stripLeadCD strips a leading "cd <dir> && " from cmd (with or without
// the trailing &&). Returns the remainder after the cd.
func stripLeadCD(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if !strings.HasPrefix(cmd, "cd ") {
		return cmd
	}
	rest := cmd[3:]
	spaceIdx := strings.IndexByte(rest, ' ')
	if spaceIdx < 0 {
		return cmd
	}
	// Skip the dir (first token of rest)
	after := rest[spaceIdx+1:]
	after = strings.TrimSpace(after)
	after = strings.TrimPrefix(after, "&&")
	after = strings.TrimSpace(after)
	if after == "" {
		return cmd
	}
	return after
}

// stripBogusLeadCD checks whether cmd starts with "cd <dir> && " where <dir>
// is not a valid top-level project directory; if so, strips the cd prefix.
func stripBogusLeadCD(cmd string, validTopDirs map[string]bool) string {
	cmd = strings.TrimSpace(cmd)
	if !strings.HasPrefix(cmd, "cd ") {
		return cmd
	}
	rest := strings.TrimSpace(cmd[3:])
	spaceIdx := strings.IndexByte(rest, ' ')
	if spaceIdx < 0 {
		return cmd
	}
	dir := rest[:spaceIdx]

	firstComp := dir
	if slashIdx := strings.IndexByte(dir, '/'); slashIdx >= 0 {
		firstComp = dir[:slashIdx]
	}

	if validTopDirs[firstComp] || firstComp == "." || firstComp == ".." {
		return cmd
	}

	after := strings.TrimSpace(rest[spaceIdx:])
	after = strings.TrimPrefix(after, "&& ")
	after = strings.TrimSpace(after)
	if after == "" {
		return cmd
	}

	if slashIdx := strings.IndexByte(dir, '/'); slashIdx >= 0 {
		subDir := dir[slashIdx+1:]
		if subDir != "" {
			after = "cd " + subDir + " && " + after
		}
	}

	// If after still starts with a bogus prefix (e.g. "finally/backend && pytest")
	// that is NOT a valid top-level dir, strip it too. This catches cases where
	// the LLM used the rig name as a path prefix in the verify command.
	afterTrim := strings.TrimSpace(after)
	if spaceIdx := strings.IndexByte(afterTrim, ' '); spaceIdx >= 0 {
		firstToken := afterTrim[:spaceIdx]
		if !validTopDirs[firstToken] && firstToken != "." && firstToken != ".." {
			if slashIdx := strings.IndexByte(firstToken, '/'); slashIdx >= 0 {
				subDir := firstToken[slashIdx+1:]
				if subDir != "" {
					remainder := strings.TrimSpace(afterTrim[spaceIdx+1:])
					remainder = strings.TrimPrefix(remainder, "&& ")
					if validTopDirs[subDir] || validTopDirs[strings.Split(subDir, "/")[0]] {
						after = "cd " + subDir + " && " + remainder
					} else {
						after = remainder
					}
				}
			}
		}
	}

	return after
}

// stripNestedBogusCD removes any remaining "cd <bogus>/" references buried inside
// a verify command (e.g. "cd frontend && cd finally/frontend && npm install").
// It uses the same validTopDirs check: if a cd target's first component is not
// a known project directory, the cd is replaced with its subpath or removed.
// stripNestedBogusCD removes "cd <bogus_dir>" segments from a &&-chained command
// where <bogus_dir>'s first component is not a known project directory. It targets
// patterns like "cd frontend && cd finally/frontend && npm install" and rewrites
// them to "cd frontend && npm install".
func stripNestedBogusCD(cmd string, validTopDirs map[string]bool) string {
	for {
		idx := strings.Index(cmd, "&& cd ")
		if idx < 0 {
			break
		}
		segment := cmd[idx+5:]
		spaceIdx := strings.IndexByte(segment[3:], ' ')
		if spaceIdx < 0 {
			break
		}
		segment = segment[3:]
		spaceIdx = strings.IndexByte(segment, ' ')
		dir := segment[:spaceIdx]
		firstComp := dir
		if slashIdx := strings.IndexByte(dir, '/'); slashIdx >= 0 {
			firstComp = dir[:slashIdx]
		}
		if validTopDirs[firstComp] || firstComp == "." || firstComp == ".." {
			break
		}
		afterCD := segment[spaceIdx+1:]
		afterCD = strings.TrimPrefix(strings.TrimSpace(afterCD), "&& ")
		afterCD = strings.TrimSpace(afterCD)
		if afterCD == "" {
			return strings.TrimSpace(cmd[:idx])
		}
		if slashIdx := strings.IndexByte(dir, '/'); slashIdx >= 0 {
			subDir := dir[slashIdx+1:]
			if subDir != "" {
				cmd = strings.TrimSpace(cmd[:idx]) + " && cd " + subDir + " && " + afterCD
				continue
			}
		}
		cmd = strings.TrimSpace(cmd[:idx]) + " && " + afterCD
	}
	return cmd
}

// IsPlaceholderOrMismatchedCommand returns true when the verify command for a
// phase should be regenerated because it's either an echo-based placeholder or
// doesn't test the actual file types present in the phase (e.g. Docker files
// but no docker-compose/docker command; shell scripts but no test -f/sh check).
func IsPlaceholderOrMismatchedCommand(cmd string, p *DeliveryPhase) bool {
	lower := strings.ToLower(cmd)

	// Extract the actual action: strip leading "cd <dir> && " since that's
	// directory setup, not the actual test. Then check if what remains is
	// just echo (placeholder) or has real test commands.
	action := stripLeadCD(lower)
	action = strings.TrimSpace(action)

	// If the action starts with echo, it's a placeholder (no real tests).
	if strings.HasPrefix(action, "echo") {
		return true
	}
	// If the entire command is echo (after stripping cd), it's a placeholder.
	if action == "" || strings.TrimSpace(lower) == strings.TrimSpace(cmd) {
		// Fall through to content-based checks below.
	}

	// Check if cd dirs don't match phase files.
	if phaseVerifyDirMismatch(p) {
		return true
	}

	// Check phase content vs command keywords. If the phase has distinct file
	// types and the command doesn't reference them, it's likely wrong.
	hasDocker := false
	hasPlaywright := false
	hasScripts := false
	hasGo := false
	hasPython := false
	hasNode := false
	for _, f := range p.RequiredFiles {
		lf := strings.ToLower(f)
		if strings.Contains(lf, "dockerfile") || strings.Contains(lf, "docker-compose") || strings.HasSuffix(lf, ".dockerfile") {
			hasDocker = true
		}
		if strings.Contains(lf, "playwright") {
			hasPlaywright = true
		}
		if strings.HasSuffix(lf, ".sh") || strings.HasSuffix(lf, ".ps1") || strings.HasSuffix(lf, ".bat") {
			hasScripts = true
		}
		if strings.HasSuffix(lf, ".go") {
			hasGo = true
		}
		if strings.HasSuffix(lf, ".py") {
			hasPython = true
		}
		if strings.HasSuffix(lf, ".ts") || strings.HasSuffix(lf, ".tsx") || strings.HasSuffix(lf, ".js") || strings.HasSuffix(lf, ".jsx") || strings.HasSuffix(lf, ".mjs") {
			hasNode = true
		}
	}

	// For phases with scripts, Docker, or testable files, verify the command's
	// referenced file paths actually exist in the phase's required files.
	if !commandPathsMatchPhaseFiles(cmd, p.RequiredFiles) {
		return true
	}

	if hasDocker && !strings.Contains(lower, "docker") && !strings.Contains(lower, "test -f") {
		return true
	}
	if hasPlaywright && !strings.Contains(lower, "playwright") {
		return true
	}
	if hasScripts && !strings.Contains(lower, "test -f") && !strings.Contains(lower, "sh") && !strings.Contains(lower, "chmod") {
		return true
	}
	if hasGo && !strings.Contains(lower, "go ") {
		return true
	}
	// A runtime smoke command (uvicorn/gunicorn/flask + curl + loopback) is a
	// legitimate Python phase verify even without "python"/"pytest" in it.
	if hasPython && !strings.Contains(lower, "python") && !strings.Contains(lower, "pytest") &&
		!hasRuntimeSmokeCommand(cmd) {
		return true
	}
	if hasNode && !strings.Contains(lower, "npm") && !strings.Contains(lower, "npx") && !strings.Contains(lower, "tsc") && !strings.Contains(lower, "node ") {
		return true
	}

	return false
}

// commandPathsMatchPhaseFiles checks whether path-like references in the verify
// command (test -f <path>, cd <dir>) match at least one required file in the
// phase. Returns false when the command references paths/dirs that don't exist
// in the phase (e.g. "test -f finally/scripts/start_mac.sh" when the file is at
// "scripts/start_mac.sh"). Returns true if command has no paths to check.
func commandPathsMatchPhaseFiles(cmd string, files []string) bool {
	lower := strings.ToLower(cmd)
	re := regexp.MustCompile(`(?:^|\b)(?:cd\s+|test -f\s+)(\S+)`)
	matches := re.FindAllStringSubmatch(lower, -1)
	if len(matches) == 0 {
		return true
	}
	// Build top-level dirs from required files.
	topDirs := make(map[string]bool)
	fileSet := make(map[string]bool, len(files))
	for _, f := range files {
		lf := strings.ToLower(f)
		fileSet[lf] = true
		if idx := strings.IndexByte(lf, '/'); idx > 0 {
			topDirs[lf[:idx]] = true
		}
	}
	// Check each path reference against files and top-level dirs.
	for _, m := range matches {
		path := m[1]
		if path == "." || path == ".." || strings.HasPrefix(path, "/") {
			continue
		}
		// Extract first path component.
		firstComp := path
		if idx := strings.IndexByte(path, '/'); idx >= 0 {
			firstComp = path[:idx]
		}
		// If first component is a known top-level dir, the path is valid.
		if topDirs[firstComp] {
			continue
		}
		// Check exact file match.
		if fileSet[path] || fileSet[firstComp+"/"] {
			continue
		}
		// Path's first component doesn't match any file/dir — bogus.
		return false
	}
	return true
}

// phaseVerifyDirMismatch returns true when a phase's verify command references
// subdirectories that don't exist among its required files, indicating the
// command was generated for a different directory layout than what the phase
// actually produces.
func phaseVerifyDirMismatch(p *DeliveryPhase) bool {
	cmd := strings.TrimSpace(p.QAVerifyCommand)
	if cmd == "" {
		return false
	}
	re := regexp.MustCompile(`\bcd\s+(?:\./)?(\S+)`)
	matches := re.FindAllStringSubmatch(cmd, -1)
	dirs := make([]string, 0, len(matches))
	for _, m := range matches {
		dir := m[1]
		if dir == "." {
			continue
		}
		dirs = append(dirs, dir)
	}
	if len(dirs) == 0 {
		return false
	}
nextDir:
	for _, d := range dirs {
		prefix := d + "/"
		for _, f := range p.RequiredFiles {
			if strings.HasPrefix(f, prefix) {
				continue nextDir
			}
		}
		return true
	}
	return false
}

// validatePhaseVerifyCommands ensures each delivery phase's QA verify command can run
// with the files provided by that phase and its dependencies. If a phase's command
// references tools/scripts not available (e.g., "npm test" without test script in
// package.json), it adds the missing files to the phase's required_files.
func validatePhaseVerifyCommands(v WorkflowValidation) WorkflowValidation {
	if !v.HasPhasedDelivery() {
		return v
	}
	for i := range v.DeliveryPhases {
		cmd := strings.TrimSpace(v.DeliveryPhases[i].QAVerifyCommand)
		if cmd == "" {
			continue
		}
		// npm test requires package.json with "test" script
		if strings.Contains(cmd, "npm test") || strings.Contains(cmd, "npm run test") {
			hasPackageJSON := false
			for _, f := range v.DeliveryPhases[i].RequiredFiles {
				if strings.HasSuffix(f, "package.json") {
					hasPackageJSON = true
					break
				}
			}
			if !hasPackageJSON {
				// Check earlier phases for package.json
				found := false
				for j := 0; j < i; j++ {
					for _, f := range v.DeliveryPhases[j].RequiredFiles {
						if strings.HasSuffix(f, "package.json") {
							found = true
							break
						}
					}
					if found {
						break
					}
				}
				if !found && len(v.DeliveryPhases[i].RequiredFiles) > 0 {
					// Find the correct package.json location - for frontend, it should be at the project root
					// not in src/. Look for the most likely project root from the phase's files.
					base := findProjectRootForNPM(v.DeliveryPhases[i].RequiredFiles)
					if base == "" {
						base = filepath.Dir(v.DeliveryPhases[i].RequiredFiles[0])
					}
					if base == "." {
						v.DeliveryPhases[i].RequiredFiles = append(v.DeliveryPhases[i].RequiredFiles, "package.json")
					} else {
						v.DeliveryPhases[i].RequiredFiles = append(v.DeliveryPhases[i].RequiredFiles, base+"/package.json")
					}
				}
			}
		}
		// pytest may need a config file, but only when the project has no other
		// Python manifest (requirements.txt) and no pyproject.toml. Injecting
		// pyproject.toml into a requirements.txt-based project creates a phantom
		// bead and contradicts SPEC layouts that say "no extra files or
		// abstractions". pytest runs fine from requirements.txt alone. The check
		// must span the whole profile (UnionRequiredFiles), not just this phase
		// and its predecessors — a smoke-test phase listing only main.py must not
		// trigger injection when an earlier phase owns requirements.txt.
		if strings.Contains(cmd, "pytest") {
			union := v.UnionRequiredFiles()
			hasPyproject := false
			hasRequirements := false
			for _, f := range union {
				f = filepath.ToSlash(strings.TrimSpace(f))
				if strings.HasSuffix(f, "pyproject.toml") {
					hasPyproject = true
				}
				if strings.HasSuffix(f, "requirements.txt") {
					hasRequirements = true
				}
			}
			if hasPyproject || hasRequirements {
				continue
			}
			if len(v.DeliveryPhases[i].RequiredFiles) > 0 {
				base := filepath.Dir(v.DeliveryPhases[i].RequiredFiles[0])
				if base == "." {
					v.DeliveryPhases[i].RequiredFiles = append(v.DeliveryPhases[i].RequiredFiles, "pyproject.toml")
				} else {
					v.DeliveryPhases[i].RequiredFiles = append(v.DeliveryPhases[i].RequiredFiles, base+"/pyproject.toml")
				}
			}
		}
	}
	return v
}

// findProjectRootForNPM determines the correct project root for npm commands.
// It avoids placing package.json in src/ subdirectories by finding the most
// likely project root from the phase's required files.
func findProjectRootForNPM(files []string) string {
	// Common patterns for project roots in frontend projects
	for _, f := range files {
		dir := filepath.Dir(f)
		// Look for common frontend project root indicators
		parts := strings.Split(dir, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] == "frontend" || parts[i] == "app" || parts[i] == "web" {
				// The project root is likely the parent of this directory
				if i > 0 {
					return strings.Join(parts[:i+1], "/")
				}
			}
		}
	}
	// Fallback: find the shortest common directory path that isn't "src"
	for _, f := range files {
		dir := filepath.Dir(f)
		parts := strings.Split(dir, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			if parts[i] == "src" {
				if i > 0 {
					return strings.Join(parts[:i], "/")
				}
			}
		}
	}
	return ""
}

// validatePhaseVerifyCommandsAgainstFiles checks if a phase's verify command
// references files that aren't available in the current or previous phases.
// If so, rewrites the command to a safe stack-appropriate default.
func validatePhaseVerifyCommandsAgainstFiles(v WorkflowValidation) WorkflowValidation {
	if !v.HasPhasedDelivery() || len(v.DeliveryPhases) == 0 {
		return v
	}
	// Track files available up to each phase
	availableFiles := map[string]bool{}
	for i := range v.DeliveryPhases {
		p := &v.DeliveryPhases[i]
		cmd := strings.TrimSpace(p.QAVerifyCommand)
		if cmd == "" {
			// Add this phase's files to available and continue
			for _, f := range p.RequiredFiles {
				availableFiles[filepath.ToSlash(strings.TrimSpace(f))] = true
			}
			continue
		}
		// Check if command references files not yet available
		if referencesMissingFiles(cmd, availableFiles) {
			// Rewrite to stack-appropriate safe default
			p.QAVerifyCommand = defaultQAVerifyForPhase(p, v.LayoutRoot)
			log.Printf("[clamp] phase %q: verify command references future-phase files, replaced with %q", p.ID, p.QAVerifyCommand)
		}
		// Add this phase's files to available for next phases
		for _, f := range p.RequiredFiles {
			availableFiles[filepath.ToSlash(strings.TrimSpace(f))] = true
		}
	}
	return v
}

// referencesMissingFiles checks if a verify command references files
// (via go run, curl, or path patterns) that aren't in availableFiles.
func referencesMissingFiles(cmd string, availableFiles map[string]bool) bool {
	lower := strings.ToLower(cmd)

	// Check for go run <path>
	if strings.Contains(lower, "go run") {
		// Extract path after "go run"
		idx := strings.Index(lower, "go run")
		if idx >= 0 {
			after := cmd[idx+len("go run"):]
			fields := strings.Fields(after)
			for _, f := range fields {
				f = strings.TrimSpace(f)
				if f == "" || strings.HasPrefix(f, "-") {
					continue
				}
				if strings.HasSuffix(f, ".go") {
					if !availableFiles[filepath.ToSlash(f)] {
						return true
					}
				}
			}
		}
	}

	// Check for python uvicorn/flask/... commands that start a server
	// These START the server in the same command, so they don't need pre-existing files
	// Only flag if it references a .py file that doesn't exist
	if strings.Contains(lower, "uvicorn") || strings.Contains(lower, "flask run") ||
		strings.Contains(lower, "gunicorn") || strings.Contains(lower, "hypercorn") {
		// These start the server - extract any .py file argument
		fields := strings.Fields(cmd)
		for _, f := range fields {
			f = strings.TrimSpace(f)
			if strings.HasSuffix(f, ".py") && !strings.HasPrefix(f, "-") {
				if !availableFiles[filepath.ToSlash(f)] {
					return true
				}
			}
		}
	}

	// Check for curl to paths that imply server endpoints
	// (heuristic: if cmd has curl but no server-start, it expects server to exist)
	if strings.Contains(lower, "curl ") {
		// Check if the command STARTS a server in the same command
		hasServerStart := strings.Contains(lower, "go run") ||
			strings.Contains(lower, "uvicorn") ||
			strings.Contains(lower, "flask run") ||
			strings.Contains(lower, "gunicorn") ||
			strings.Contains(lower, "hypercorn") ||
			strings.Contains(lower, "docker compose") ||
			strings.Contains(lower, "docker-compose")

		if !hasServerStart {
			// curl without server startup implies server binary already exists
			// Look for go build patterns that would create server
			hasBuild := strings.Contains(lower, "go build") && (strings.Contains(lower, "main.go") || strings.Contains(lower, "cmd/server"))
			if !hasBuild {
				// Check if server binary would be built from available files
				hasServerSource := false
				for f := range availableFiles {
					if strings.HasSuffix(f, "cmd/server/main.go") || strings.HasSuffix(f, "main.go") ||
						strings.HasSuffix(f, "main.py") || strings.HasSuffix(f, "app.py") ||
						strings.HasSuffix(f, "server.py") {
						hasServerSource = true
						break
					}
				}
				if !hasServerSource {
					return true
				}
			}
		}
	}
	return false
}

// StripNonFileRequiredEntries removes entries from required_files that are
// clearly not file paths — e.g. code fragments like "database.init_db()",
// "cash_balance=10000.0", or bare numbers like "10000.0" that the LLM
// sometimes injects instead of actual file paths. Also filters Go stdlib
// symbols (httptest.NewServer, http.ResponseWriter), version numbers (1.21),
// and method references (json.Marshal).
func StripNonFileRequiredEntries(files []string) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if strings.Contains(f, "(") || strings.Contains(f, "=") {
			continue
		}
		// Filter Go stdlib symbols: pkg.Type or pkg.Method (capital after dot)
		if goStdlibSymbolRE.MatchString(f) {
			continue
		}
		// Filter bare version numbers like "1.21", "1.22"
		if versionNumberRE.MatchString(f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

var (
	// Matches Go standard library symbols: pkg.Type, pkg.Method (capital letter after dot)
	goStdlibSymbolRE = regexp.MustCompile(`^[a-z][a-z0-9]*\.[A-Z][a-zA-Z0-9]*$`)
	// Matches version numbers like 1.21, 1.22, 3.10
	versionNumberRE = regexp.MustCompile(`^\d+\.\d+$`)
)

// deduplicateRequiredFiles removes obviously incorrect nested paths when the
// correct parent path is already present. E.g., if both "X/package.json" and
// "X/src/package.json" are in the list, the src/ one is wrong (the LLM placed
// the file at the wrong depth).
func deduplicateRequiredFiles(files []string) []string {
	// Build a set of all files for O(1) lookup
	fileSet := make(map[string]bool, len(files))
	for _, f := range files {
		fileSet[f] = true
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		dir := filepath.Dir(f)
		base := filepath.Base(f)
		parts := strings.Split(dir, "/")
		// Walk up one level: if X/Y/file exists and X/file also exists, skip this one
		skip := false
		if len(parts) >= 2 {
			parentDir := strings.Join(parts[:len(parts)-1], "/")
			parentPath := parentDir + "/" + base
			if fileSet[parentPath] && parentPath != f {
				skip = true
			}
		}
		if !skip {
			out = append(out, f)
		}
	}
	return out
}

// ClampProfileValidationForRig applies ClampProfileValidation and, when architecture.md exists,
// aligns layout_root with paths documented in the architecture (flat mayor/rig worktrees).
func ClampProfileValidationForRig(townRoot, rig string, v WorkflowValidation) WorkflowValidation {
	v = ClampProfileValidation(v)
	if townRoot == "" || rig == "" {
		return v
	}
	archPath := filepath.Join(townRoot, rig, "mayor", "rig", "architecture.md")
	return AlignProfileLayoutWithArchitecture(v, archPath)
}

// capArchitectureBytesForSmallRig lowers min_architecture_bytes when required_files is a short list.
func capArchitectureBytesForSmallRig(v WorkflowValidation) WorkflowValidation {
	n := len(v.RequiredFiles)
	if v.HasPhasedDelivery() {
		if active := v.ActiveRequiredFiles(); len(active) > 0 {
			n = len(active)
		}
	}
	if n == 0 || n > smallRigRequiredFileCap {
		return v
	}
	if v.MinArchitectureBytes > SmallRigMaxArchitectureBytes {
		v.MinArchitectureBytes = SmallRigMaxArchitectureBytes
	}
	return v
}

// NormalizeLayoutProfile prefixes required_files with layout_root and ensures Go
// qa_verify_command runs from the module directory when the LLM omitted cd.
func NormalizeLayoutProfile(v WorkflowValidation) WorkflowValidation {
	v.QAVerifyCommand = NormalizeDockerQACommand(v.QAVerifyCommand)
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].QAVerifyCommand = NormalizeDockerQACommand(v.DeliveryPhases[i].QAVerifyCommand)
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" || layout == "." {
		return v
	}
	if len(v.RequiredFiles) > 0 {
		out := make([]string, 0, len(v.RequiredFiles))
		for _, f := range v.RequiredFiles {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if f == "" {
				continue
			}
			if !strings.HasPrefix(f, layout+"/") && !strings.Contains(f, "..") {
				f = layout + "/" + strings.TrimPrefix(f, "/")
			}
			out = append(out, f)
		}
		v.RequiredFiles = out
	}
	v = NormalizeDeliveryPhasesLayout(v)
	qa := strings.TrimSpace(v.QAVerifyCommand)
	if qa != "" && WorkflowUsesGo(v) {
		lower := strings.ToLower(qa)
		cdLayout := "cd " + layout
		if !strings.Contains(lower, cdLayout) && !strings.Contains(lower, "cd ./"+layout) {
			if !strings.Contains(lower, "cd ") {
				v.QAVerifyCommand = cdLayout + " && " + qa
			}
		}
	}
	return v
}

func clampArtifactBytes(value, defaultVal, floor, ceiling int64) int64 {
	if value <= 0 {
		return defaultVal
	}
	if value < floor {
		return floor
	}
	if value > ceiling {
		return defaultVal
	}
	return value
}

// WithDefaults fills empty fields from DefaultWorkflowValidation.
func (v WorkflowValidation) WithDefaults() WorkflowValidation {
	d := DefaultWorkflowValidation()
	if v.BeadTitleContains == "" {
		v.BeadTitleContains = d.BeadTitleContains
	}
	hasCustomQA := strings.TrimSpace(v.QAVerifyCommand) != "" ||
		strings.EqualFold(strings.TrimSpace(v.TestRunner), "pytest") ||
		strings.EqualFold(strings.TrimSpace(v.TestRunner), "custom")
	if v.UnittestModule == "" && !hasCustomQA {
		v.UnittestModule = d.UnittestModule
	}
	if len(v.RequiredFiles) == 0 {
		v.RequiredFiles = append([]string(nil), d.RequiredFiles...)
	}
	if v.MinArchitectureBytes <= 0 {
		v.MinArchitectureBytes = d.MinArchitectureBytes
	}
	if v.MinPlanBytes <= 0 {
		v.MinPlanBytes = d.MinPlanBytes
	}
	if v.MinTestPlanBytes <= 0 {
		v.MinTestPlanBytes = d.MinTestPlanBytes
	}
	if v.MaxReviewRetries <= 0 {
		v.MaxReviewRetries = d.MaxReviewRetries
	}
	return v
}

// MergeValidation overlays template and task validation onto defaults.
func MergeValidation(tpl *WorkflowTemplate, task *Task) WorkflowValidation {
	v := DefaultWorkflowValidation()
	if tpl != nil {
		v = mergeValidationFields(v, tpl.Validation)
	}
	if task != nil {
		v = mergeValidationFields(v, task.Validation)
	}
	return v.WithDefaults()
}

func mergeValidationFields(base, overlay WorkflowValidation) WorkflowValidation {
	if overlay.LayoutRoot != "" {
		base.LayoutRoot = overlay.LayoutRoot
	}
	if overlay.BeadTitleContains != "" {
		base.BeadTitleContains = overlay.BeadTitleContains
	}
	if overlay.UnittestModule != "" {
		base.UnittestModule = overlay.UnittestModule
	}
	if overlay.QAVerifyCommand != "" {
		base.QAVerifyCommand = overlay.QAVerifyCommand
	}
	if overlay.TestRunner != "" {
		base.TestRunner = overlay.TestRunner
	}
	if overlay.SpecSummary != "" {
		base.SpecSummary = overlay.SpecSummary
	}
	if len(overlay.RequiredFiles) > 0 {
		base.RequiredFiles = append([]string(nil), overlay.RequiredFiles...)
	}
	if len(overlay.DeliveryPhases) > 0 {
		base.DeliveryPhases = append([]DeliveryPhase(nil), overlay.DeliveryPhases...)
	}
	if overlay.ActivePhaseIDField != "" {
		base.ActivePhaseIDField = overlay.ActivePhaseIDField
	}
	if len(overlay.CompletedPhaseIDsField) > 0 {
		base.CompletedPhaseIDsField = append([]string(nil), overlay.CompletedPhaseIDsField...)
	}
	if overlay.MinArchitectureBytes > 0 {
		base.MinArchitectureBytes = overlay.MinArchitectureBytes
	}
	if overlay.MinPlanBytes > 0 {
		base.MinPlanBytes = overlay.MinPlanBytes
	}
	if overlay.MinImplementationFileBytes > 0 {
		base.MinImplementationFileBytes = overlay.MinImplementationFileBytes
	}
	if overlay.MinSubstantiveLines > 0 {
		base.MinSubstantiveLines = overlay.MinSubstantiveLines
	}
	if overlay.MinTestPlanBytes > 0 {
		base.MinTestPlanBytes = overlay.MinTestPlanBytes
	}
	if overlay.UICommand != "" {
		base.UICommand = overlay.UICommand
	}
	if overlay.MaxReviewRetries > 0 {
		base.MaxReviewRetries = overlay.MaxReviewRetries
	}
	if overlay.PythonVenvDir != "" {
		base.PythonVenvDir = overlay.PythonVenvDir
	}
	if overlay.DevServerPort > 0 {
		base.DevServerPort = overlay.DevServerPort
	}
	base.QAVerifyCommand = NormalizePytestCommand(strings.TrimSpace(base.QAVerifyCommand))
	return base
}

// SubstituteVars replaces {{key}} in validation string fields.
func (v WorkflowValidation) SubstituteVars(vars map[string]string) WorkflowValidation {
	if len(vars) == 0 {
		return v
	}
	v.LayoutRoot = SubstituteVars(v.LayoutRoot, vars)
	v.BeadTitleContains = SubstituteVars(v.BeadTitleContains, vars)
	v.UnittestModule = SubstituteVars(v.UnittestModule, vars)
	v.QAVerifyCommand = SubstituteVars(v.QAVerifyCommand, vars)
	v.TestRunner = SubstituteVars(v.TestRunner, vars)
	v.UICommand = SubstituteVars(v.UICommand, vars)
	v.SpecSummary = SubstituteVars(v.SpecSummary, vars)
	for i, f := range v.RequiredFiles {
		v.RequiredFiles[i] = SubstituteVars(f, vars)
	}
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].ID = SubstituteVars(v.DeliveryPhases[i].ID, vars)
		v.DeliveryPhases[i].Title = SubstituteVars(v.DeliveryPhases[i].Title, vars)
		v.DeliveryPhases[i].QAVerifyCommand = SubstituteVars(v.DeliveryPhases[i].QAVerifyCommand, vars)
		v.DeliveryPhases[i].SpecFocus = SubstituteVars(v.DeliveryPhases[i].SpecFocus, vars)
		for j, f := range v.DeliveryPhases[i].RequiredFiles {
			v.DeliveryPhases[i].RequiredFiles[j] = SubstituteVars(f, vars)
		}
	}
	v.ActivePhaseIDField = SubstituteVars(v.ActivePhaseIDField, vars)
	return v
}

// RequirementsFilePath returns the first requirements.txt or pyproject.toml from the profile, if any.
func (v WorkflowValidation) RequirementsFilePath() string {
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if strings.HasSuffix(f, "requirements.txt") || strings.HasSuffix(f, "pyproject.toml") {
			return f
		}
	}
	return ""
}

// LayoutRootDir returns the profile layout_root for path hints, or "." when unset.
func (v WorkflowValidation) LayoutRootDir() string {
	if l := strings.TrimSpace(v.LayoutRoot); l != "" {
		return l
	}
	return "."
}

// PromptVars returns keys for {{bead_title_contains}}, {{unittest_module}}, etc. in prompt files.
func (v WorkflowValidation) PromptVars() map[string]string {
	req := v.RequirementsFilePath()
	scoped := v.ForActivePhase()
	activeFiles := scoped.RequiredFiles
	allFiles := v.UnionRequiredFiles()
	if len(allFiles) == 0 {
		allFiles = append([]string(nil), activeFiles...)
	}
	phaseQA := scoped.QAVerifyCommand
	activeID := v.ActivePhaseID()
	activeTitle := ""
	if p, ok := v.ActivePhase(); ok {
		activeTitle = strings.TrimSpace(p.Title)
		if activeID == "" {
			activeID = strings.TrimSpace(p.ID)
		}
	}
	return map[string]string{
		"layout_root":                     v.LayoutRoot,
		"bead_title_contains":             v.BeadTitleContains,
		"unittest_module":                 v.UnittestModule,
		"qa_verify_command":               phaseQA,
		"phase_qa_verify_command":         phaseQA,
		"test_runner":                     v.TestRunner,
		"required_files":                  strings.Join(activeFiles, ", "),
		"all_required_files":              strings.Join(allFiles, ", "),
		"active_phase_id":                 activeID,
		"active_phase_title":              activeTitle,
		"delivery_phase_count":            fmt.Sprintf("%d", len(v.DeliveryPhases)),
		"phase_scope_note":                v.PhaseScopeNote(),
		"integration_contract_scope_note": v.IntegrationContractScopeNote(),
		"requirements_file":               req,
		"spec_summary":                    v.SpecSummary,
		"unittest_command_hint":           scoped.QAVerifyHint(),
		"implementation_verify_hint":      "(resolved per rig at fetch_task — use go build until server main exists)",
		"project_setup_verify_hint":       scoped.ProjectSetupVerifyHint(),
		"project_setup_failure_hint":      ProjectSetupFailureHint(scoped),
		"project_setup_stack_kind":        ProjectSetupStackKind(scoped),
		"python_venv_dir":                 v.PythonVenvRelDir(),
		"min_architecture_bytes":          fmt.Sprintf("%d", v.MinArchitectureBytes),
		"min_plan_bytes":                  fmt.Sprintf("%d", v.MinPlanBytes),
		"min_test_plan_bytes":             fmt.Sprintf("%d", v.MinTestPlanBytes),
		"ui_command":                      v.UICommand,
		"max_review_retries":              fmt.Sprintf("%d", v.MaxReviewRetries),
		"min_implementation_file_bytes":   fmt.Sprintf("%d", StubCheckOptionsFromValidation(v).MinFileBytes),
		"min_substantive_lines":           fmt.Sprintf("%d", StubCheckOptionsFromValidation(v).MinSubstantiveLines),
		"bead_id_example":                 beadIDExample(v),
		"static_url_contract_guidance":    RigFlowStaticURLContractGuidance,
		"static_url_contract_short":       RigFlowStaticURLContractShort,
		"target_os":                       runtime.GOOS,
		"target_arch":                     runtime.GOARCH,
	}
}

func beadIDExample(v WorkflowValidation) string {
	// Filled from rig prefix at payload build when available; fallback for templates.
	if p := strings.TrimSpace(v.BeadTitleContains); p != "" {
		return "<id-from-bd-list>"
	}
	return "<id-from-bd-list>"
}

// ForbiddenRigRootBasenames lists mayor/rig files that must not exist outside subdirs during design
// (e.g. layout_root/pkg/module.py → forbid module.py at rig root).
func (v WorkflowValidation) ForbiddenRigRootBasenames() []string {
	seen := make(map[string]bool)
	var out []string
	for _, f := range v.RequiredFiles {
		if strings.Contains(f, "/") {
			base := filepath.Base(f)
			if base != "" && base != "/" && base != "." && !seen[base] {
				seen[base] = true
				out = append(out, base)
			}
		}
	}
	return out
}

// ImplementationVerifyHint returns verify text for polecat prompts (system/failure hints).
// Always compile-only — per-bead verify (incl. go run/curl on non-go.mod beads) is enforced by gt-agent.
func (v WorkflowValidation) ImplementationVerifyHint(mayorRigDir string) string {
	if WorkflowUsesGo(v) {
		return GoCompileOnlyVerifyCommand(v, mayorRigDir)
	}
	if WorkflowUsesPython(v) {
		return PythonVerifyCommand(v)
	}
	if WorkflowUsesDocker(v) {
		return DockerImplementationVerifyCommandForBead(v.ForActivePhase(), mayorRigDir, "")
	}
	return v.UnittestCommandHint()
}

// ProjectSetupVerifyHint returns the verify command agents should run in project_setup.
func (v WorkflowValidation) ProjectSetupVerifyHint() string {
	if WorkflowUsesGo(v) {
		return GoProjectSetupVerifyCommand(v, "")
	}
	// Check Node.js before Python so dual-stack rigs (Python backend + Node frontend)
	// scope each delivery phase to its actual stack.
	if WorkflowUsesNodeJS(v) {
		return NodeProjectSetupVerifyCommand(v)
	}
	if WorkflowUsesPython(v) {
		return PythonProjectSetupVerifyCommand(v)
	}
	if WorkflowUsesDocker(v) {
		scoped := v.ForActivePhase()
		layout := strings.Trim(strings.TrimSpace(scoped.LayoutRoot), "/")
		if layout == "" {
			layout = "."
		}
		return dockerVerifyWithLayout(scoped.ActivePhaseQAVerifyCommand(), layout)
	}
	return v.UnittestCommandHint()
}

// QAVerifyHint returns the suggested QA command for error messages.
func (v WorkflowValidation) QAVerifyHint() string {
	// For Python workflows, use PythonVerifyCommand which handles venv and layout correctly
	if WorkflowUsesPython(v) {
		return PythonVerifyCommand(v)
	}
	// For other workflows, use the scoped QAVerifyCommand which already has
	// phase-specific overrides applied (e.g., Go mod phase uses "go mod download").
	cmd := strings.TrimSpace(v.QAVerifyCommand)
	if cmd == "" {
		return v.UnittestCommandHint()
	}
	return NormalizePytestCommand(cmd)
}

// UnittestCommandHint returns the suggested QA command for error messages.
func (v WorkflowValidation) UnittestCommandHint() string {
	if q := strings.TrimSpace(v.QAVerifyCommand); q != "" {
		return NormalizePytestCommand(q)
	}
	mod := strings.TrimSpace(v.UnittestModule)
	if mod == "" {
		mod = DefaultWorkflowValidation().UnittestModule
	}
	return "python3 -m unittest " + mod
}

// NormalizePytestCommand rewrites bare `pytest` to `python3 -m pytest` for agent PATHs without a pytest shim.
func NormalizePytestCommand(cmd string) string {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "pytest") {
		return cmd
	}
	if strings.Contains(lower, "import pytest") {
		return cmd
	}
	if strings.Contains(lower, "-c ") && strings.Contains(lower, "import ") {
		return cmd
	}
	if strings.Contains(lower, "python3 -m pytest") || strings.Contains(lower, "python -m pytest") {
		return cmd
	}
	if strings.Contains(lower, "pip install") {
		return cmd
	}
	re := regexp.MustCompile(`(?i)(^|[;&|]\s*|\s+)pytest\b`)
	return re.ReplaceAllString(cmd, `${1}python3 -m pytest`)
}

// dockerComposeUpRe matches a docker compose invocation that runs `up`, capturing
// the invocation prefix (binary + `-f` flags) so a preceding no-cache build step
// can reuse it. Trailing `up` flags are captured up to the next `&`, `;`, or
// newline so `up --exit-code-from playwright` is preserved.
var dockerComposeUpRe = regexp.MustCompile(
	`((?:docker-compose|docker\s+compose)(?:\s+-\w+(?:\s+[^\s&;]+)?)*)\s+up\b([^\n&;]*)`,
)

// NormalizeDockerQACommand ensures docker compose E2E verify commands rebuild the
// app image from scratch before starting containers. A plain `compose up` silently
// reuses whatever image is already tagged — usually a stale build from a previous
// run — so QA tests old code and fails for reasons unrelated to the current
// implementation. Inserting `build --no-cache` forces the command to verify the
// code on disk, not a cached image.
func NormalizeDockerQACommand(cmd string) string {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "docker compose") && !strings.Contains(lower, "docker-compose") {
		return cmd
	}
	if strings.Contains(lower, "build --no-cache") {
		return cmd
	}
	return dockerComposeUpRe.ReplaceAllStringFunc(cmd, func(match string) string {
		sub := dockerComposeUpRe.FindStringSubmatch(match)
		if len(sub) != 3 {
			return match
		}
		prefix := sub[1]
		flags := strings.ReplaceAll(sub[2], "--build", "")
		flags = strings.Join(strings.Fields(flags), " ")
		if flags != "" {
			flags = " " + flags
		}
		// Insert no-cache build before up; remove old test-app:latest first so it never
		// becomes dangling. Append image prune to clean any remaining dangling layers.
		return prefix + " build --no-cache && " + prefix + " up" + flags + " && docker rmi test-app:latest 2>/dev/null || true && docker image prune -f"
	})
}

// NormalizePipCommand rewrites bare `pip` to `python3 -m pip` when pip is not on PATH but python is.
func NormalizePipCommand(cmd string) string {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "pip") {
		return cmd
	}
	if strings.Contains(lower, ".venv/bin/pip") || strings.Contains(lower, "/bin/pip install") {
		return cmd
	}
	if strings.Contains(lower, "python3 -m pip") || strings.Contains(lower, "python -m pip") {
		return cmd
	}
	// "pip install --upgrade pip" — final pip is the package name, not the CLI.
	if regexp.MustCompile(`(?i)(^|[;&|]\s*)pip\s+install\b`).MatchString(lower) &&
		regexp.MustCompile(`(?i)\binstall\s+(\S+\s+)*pip\s*$`).MatchString(strings.TrimSpace(lower)) {
		return cmd
	}
	if regexp.MustCompile(`(?i)(^|[;&|]\s*)pip\s+install\s+--upgrade\s+pip\s*$`).MatchString(strings.TrimSpace(lower)) {
		return cmd
	}
	re := regexp.MustCompile(`(?i)(^|[;&|]\s*|\s+)pip\b`)
	return re.ReplaceAllString(cmd, `${1}python3 -m pip`)
}

// ValidateDeliveryPhases enforces internal consistency on delivery_phases from spec-index or hand-edited profiles.
// Rules:
//   - Phase IDs must be unique, lowercase, kebab-case
//   - depends_on must only reference phase IDs that exist in the same array
//   - Every phase MUST have a non-empty qa_verify_command
//   - Dockerfile/docker-compose files go in the final phase only
func ValidateDeliveryPhases(v WorkflowValidation) WorkflowValidation {
	if len(v.DeliveryPhases) == 0 {
		return v
	}

	// Build ID set and map
	idSet := make(map[string]bool, len(v.DeliveryPhases))
	idMap := make(map[string]*DeliveryPhase, len(v.DeliveryPhases))
	for i := range v.DeliveryPhases {
		id := strings.TrimSpace(v.DeliveryPhases[i].ID)
		if id == "" {
			// Generate from title
			id = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(v.DeliveryPhases[i].Title), " ", "-"))
			id = strings.Trim(id, "-")
		}
		// Normalize to kebab-case
		id = normalizePhaseID(id)
		// Ensure uniqueness
		base := id
		suffix := 2
		for idSet[id] {
			id = fmt.Sprintf("%s-%d", base, suffix)
			suffix++
		}
		idSet[id] = true
		v.DeliveryPhases[i].ID = id
		idMap[id] = &v.DeliveryPhases[i]
	}

	// Validate depends_on and qa_verify_command
	for i := range v.DeliveryPhases {
		p := &v.DeliveryPhases[i]

		// Validate depends_on - only keep references to existing phase IDs
		var validDeps []string
		for _, dep := range p.DependsOn {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			if idSet[dep] {
				validDeps = append(validDeps, dep)
			}
		}
		p.DependsOn = validDeps

		// Ensure qa_verify_command exists and has no placeholder
		cmd := strings.TrimSpace(p.QAVerifyCommand)
		if cmd == "" || strings.Contains(cmd, "no verify command inferred") {
			p.QAVerifyCommand = defaultQAVerifyForPhase(p, v.LayoutRoot)
		}
		// Docker compose E2E commands must rebuild with --no-cache so QA never
		// verifies a stale tagged image.
		p.QAVerifyCommand = NormalizeDockerQACommand(p.QAVerifyCommand)
	}

	// Ensure Docker/compose files only in final phase (if multiple phases)
	if len(v.DeliveryPhases) > 1 {
		lastPhase := &v.DeliveryPhases[len(v.DeliveryPhases)-1]
		for i := 0; i < len(v.DeliveryPhases)-1; i++ {
			p := &v.DeliveryPhases[i]
			var filtered []string
			for _, f := range p.RequiredFiles {
				lower := strings.ToLower(f)
				if strings.Contains(lower, "dockerfile") ||
					strings.Contains(lower, "docker-compose") ||
					strings.Contains(lower, ".dockerignore") {
					// Move to last phase
					lastPhase.RequiredFiles = append(lastPhase.RequiredFiles, f)
					continue
				}
				filtered = append(filtered, f)
			}
			p.RequiredFiles = filtered
		}
		// Deduplicate last phase
		seen := make(map[string]bool, len(lastPhase.RequiredFiles))
		deduped := make([]string, 0, len(lastPhase.RequiredFiles))
		for _, f := range lastPhase.RequiredFiles {
			if !seen[f] {
				seen[f] = true
				deduped = append(deduped, f)
			}
		}
		lastPhase.RequiredFiles = deduped
	}

	// Regenerate verify commands that are placeholders or don't match
	// the phase's actual content (e.g. Docker/scripts/Playwright phases
	// should test those files, not run unrelated stack commands).
	for i := range v.DeliveryPhases {
		p := &v.DeliveryPhases[i]
		cmd := strings.TrimSpace(p.QAVerifyCommand)
		if cmd == "" {
			continue
		}
		if IsPlaceholderOrMismatchedCommand(cmd, p) {
			p.QAVerifyCommand = defaultQAVerifyForPhase(p, v.LayoutRoot)
		}
	}

	// Upgrade echo-based fallback to smoke test for Go+web final phase
	// (only when the default replacement is still a placeholder).
	if len(v.DeliveryPhases) > 1 {
		last := &v.DeliveryPhases[len(v.DeliveryPhases)-1]
		cmd := strings.TrimSpace(last.QAVerifyCommand)
		if strings.Contains(cmd, "echo") {
			if smokeserver := finalPhaseSmokeVerifyCommand(v); smokeserver != "" {
				last.QAVerifyCommand = smokeserver
			}
		}
	}

	return v
}

func normalizePhaseID(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "_", "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	s = strings.Trim(b.String(), "-")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	if s == "" {
		s = "phase"
	}
	return s
}

// finalPhaseSmokeVerifyCommand returns a compile+smoke verify command for the final
// delivery phase of a Go+web workflow that has cmd/server/main.go and web assets.
// Returns "" when the workflow doesn't qualify (no server, no web, or not Go).
func finalPhaseSmokeVerifyCommand(v WorkflowValidation) string {
	if !WorkflowUsesGo(v) {
		return ""
	}
	hasWeb := false
	hasServer := false
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if strings.Contains(f, "/web/") && (strings.HasSuffix(f, ".html") || strings.HasSuffix(f, ".js") || strings.HasSuffix(f, ".css")) {
			hasWeb = true
		}
		if strings.HasSuffix(f, "/cmd/server/main.go") {
			hasServer = true
		}
	}
	if !hasWeb || !hasServer {
		return ""
	}
	lr := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if lr == "" || lr == "." {
		lr = "."
	}
	return fmt.Sprintf("cd %s && go build ./... && go test ./...", lr)
}

func defaultQAVerifyForPhase(p *DeliveryPhase, layoutRoot string) string {
	hasGo := false
	hasPy := false
	hasTS := false
	hasJS := false
	hasTSConfig := false
	hasNodeProject := false
	hasScripts := false
	hasDocker := false
	hasPlaywright := false
	hasSpecFiles := false
	for _, f := range p.RequiredFiles {
		if strings.HasSuffix(f, "_test.go") || strings.HasSuffix(f, ".go") {
			hasGo = true
		}
		if strings.HasSuffix(f, ".py") || strings.HasPrefix(f, "tests/") {
			hasPy = true
		}
		if strings.HasSuffix(f, ".ts") || strings.HasSuffix(f, ".tsx") || strings.Contains(f, "frontend/") {
			hasTS = true
		}
		if strings.HasSuffix(f, ".js") || strings.HasSuffix(f, ".jsx") || strings.HasSuffix(f, ".mjs") || strings.HasSuffix(f, ".cjs") {
			hasJS = true
		}
		if strings.HasSuffix(f, "tsconfig.json") {
			hasTSConfig = true
		}
		// A phase only gets npm/tsc QA when it is an actual Node project — it ships
		// package.json / lockfile / node_modules. Plain static assets (app.js, style.css
		// served by a Go/Python backend) are NOT a Node project; running npm install
		// there fails at runtime.
		base := strings.ToLower(filepath.Base(filepath.ToSlash(strings.TrimSpace(f))))
		if base == "package.json" || base == "package-lock.json" || base == "yarn.lock" ||
			base == "pnpm-lock.yaml" || strings.Contains(f, "node_modules") {
			hasNodeProject = true
		}
		if strings.HasSuffix(f, ".sh") || strings.HasSuffix(f, ".ps1") || strings.HasSuffix(f, ".bat") {
			hasScripts = true
		}
		if strings.Contains(f, "docker-compose") || strings.Contains(f, "Dockerfile") || strings.HasSuffix(f, ".dockerfile") {
			hasDocker = true
		}
		if strings.Contains(f, "playwright") {
			hasPlaywright = true
		}
		if strings.HasSuffix(f, ".spec.ts") || strings.HasSuffix(f, ".spec.tsx") || strings.HasSuffix(f, ".e2e.ts") {
			hasSpecFiles = true
		}
	}

	lr := layoutRoot
	if lr == "" {
		lr = "."
	}

	if hasDocker {
		dockerFile := ""
		for _, f := range p.RequiredFiles {
			if strings.Contains(f, "docker-compose") {
				dockerFile = f
				break
			}
		}
		if dockerFile != "" {
			relDockerFile := strings.TrimPrefix(dockerFile, lr+"/")
			return fmt.Sprintf("cd %s && test -f %s && echo 'compose file ok'", lr, relDockerFile)
		}
	}
	if hasPlaywright || hasSpecFiles {
		dir := filepath.Dir(p.RequiredFiles[0])
		for _, f := range p.RequiredFiles {
			if strings.Contains(f, "playwright.config") {
				dir = filepath.Dir(f)
				break
			}
		}
		// Integration-test phases with Playwright should use Docker/Playwright container
		// instead of npm install. The Docker container has Playwright pre-installed.
		isIntegrationTest := strings.Contains(strings.ToLower(p.ID), "integration") ||
			strings.Contains(strings.ToLower(p.Title), "integration")
		if isIntegrationTest {
		// Integration-test phases with Playwright should use Docker/Playwright container
		// via docker compose. Tear down first so stale containers cannot lock host ports.
		return fmt.Sprintf("cd %s && docker-compose down && docker-compose up --exit-code-from playwright", lr)
		}
		if dir == "." {
			return fmt.Sprintf("cd %s && npx playwright test --list", lr)
		}
		install := nodeInstallCommand("npm", p.RequiredFiles)
		// If playwright config is at layout root, don't add subdirectory
		if dir == lr {
			return fmt.Sprintf("cd %s && %s && npx playwright test --list", lr, install)
		}
		return fmt.Sprintf("cd %s/%s && %s && npx playwright test --list", lr, dir, install)
	}
	if hasScripts {
		var sb strings.Builder
		for _, f := range p.RequiredFiles {
			if strings.HasSuffix(f, ".sh") || strings.HasSuffix(f, ".ps1") {
				if sb.Len() > 0 {
					sb.WriteString(" && ")
				}
				relF := strings.TrimPrefix(f, lr+"/")
				sb.WriteString(fmt.Sprintf("test -f %s", relF))
			}
		}
		if sb.Len() > 0 {
			return fmt.Sprintf("cd %s && %s", lr, sb.String())
		}
	}
	if hasGo {
		// Only run go test if test files exist in this phase
		hasGoTests := false
		for _, f := range p.RequiredFiles {
			if strings.HasSuffix(f, "_test.go") {
				hasGoTests = true
				break
			}
		}
		if hasGoTests {
			return fmt.Sprintf("cd %s && go test ./...", lr)
		}
		return fmt.Sprintf("cd %s && go build ./...", lr)
	}
	if hasPy {
		// Only run pytest if test files exist in this phase
		hasPyTests := false
		for _, f := range p.RequiredFiles {
			if strings.HasSuffix(f, "_test.py") || strings.HasPrefix(filepath.Base(f), "test_") ||
				strings.Contains(f, "conftest.py") || strings.Contains(f, "/tests/") {
				hasPyTests = true
				break
			}
		}
		if hasPyTests {
			return fmt.Sprintf("cd %s && python -m pytest -v", lr)
		}
		// If this phase ships a server entry (main.py/app.py/server.py or .../api/),
		// default to an import check for the entry module so a missing create_app/factory
		// is caught at import time rather than at the final E2E gate.
		if mod, cwd := pythonServerEntryModule(p.RequiredFiles, lr); mod != "" {
			return fmt.Sprintf("cd %s && python -c \"import %s\"", cwd, mod)
		}
		return fmt.Sprintf("cd %s && python -c 'import sys; print(\"ok\")'", lr)
	}
	if hasTS && hasNodeProject {
		install := nodeInstallCommand("npm", p.RequiredFiles)
		return fmt.Sprintf("cd %s/frontend && %s && npx tsc --noEmit", lr, install)
	}
	if hasJS && hasTSConfig {
		install := nodeInstallCommand("npm", p.RequiredFiles)
		return fmt.Sprintf("cd %s && %s && npx tsc --noEmit", lr, install)
	}
	if hasJS && hasNodeProject {
		// Only run npm test if test files exist
		hasJSTests := false
		for _, f := range p.RequiredFiles {
			if strings.Contains(f, ".spec.ts") || strings.Contains(f, ".spec.tsx") ||
				strings.Contains(f, ".test.ts") || strings.Contains(f, ".test.tsx") ||
				strings.Contains(f, "/test/") || strings.Contains(f, "/tests/") {
				hasJSTests = true
				break
			}
		}
		install := nodeInstallCommand("npm", p.RequiredFiles)
		if hasJSTests {
			return fmt.Sprintf("cd %s && %s && npm test", lr, install)
		}
		return fmt.Sprintf("cd %s && %s && echo 'verify ok (no JS tests)'", lr, install)
	}
	return fmt.Sprintf("cd %s && echo 'verify ok (no automated tests for this phase)'", lr)
}
