package orchestrator

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// findDockerComposeFile searches required_files for a docker-compose file and returns its path.
func findDockerComposeFile(v WorkflowValidation) string {
	for _, f := range v.UnionRequiredFiles() {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if strings.HasPrefix(strings.ToLower(filepath.Base(f)), "docker-compose") {
			return f
		}
	}
	return ""
}

// DockerImplementationVerifyCommandForBead returns verify scoped to layout_root and bead path.
func DockerImplementationVerifyCommandForBead(v WorkflowValidation, mayorRigDir, beadPath string) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	lower := strings.ToLower(beadPath)

	switch {
	case strings.HasSuffix(lower, "dockerfile"):
		return dockerVerifyWithLayout("docker build -f Dockerfile .", layout)
	case strings.HasSuffix(lower, ".sh"):
		return dockerVerifyWithLayout("bash -n "+beadPath, layout)
	case strings.HasSuffix(lower, ".ps1"):
		return dockerVerifyWithLayout("test -f "+beadPath, layout)
	case strings.Contains(lower, "docker-compose"):
		// Use the actual compose file named in the bead path, relative to layout_root.
		composeFile := beadPath
		if layout != "." && strings.HasPrefix(beadPath, layout+"/") {
			composeFile = strings.TrimPrefix(beadPath, layout+"/")
		}
		return dockerVerifyWithLayout(DockerComposeCLI()+" -f "+composeFile+" config", layout)
	case IsE2ETestPath(beadPath):
		// E2E beads run the test suite; prefer the phase QA command, fall back to a compose run.
		if q := strings.TrimSpace(v.ActivePhaseQAVerifyCommand()); q != "" {
			return dockerVerifyWithLayout(q, layout)
		}
		if q := strings.TrimSpace(v.QAVerifyCommand); q != "" {
			return dockerVerifyWithLayout(q, layout)
		}
		// Find docker-compose file in required_files for this phase
		composeFile := findDockerComposeFile(v)
		if composeFile == "" {
			composeFile = "test/docker-compose.test.yml"
		}
		cli := DockerComposeCLI()
		return dockerVerifyWithLayout(cli+" -f "+composeFile+" down && "+cli+" -f "+composeFile+" up --exit-code-from playwright", layout)
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

// DockerfileExpectation captures the concrete build/runtime requirements from architecture.md.
type DockerfileExpectation struct {
	BaseImages []string // e.g. "node:20-slim", "python:3.12-slim"
	ExposePort string   // e.g. "8000"
	CmdParts   []string // e.g. "uvicorn", "backend.main:app", "--port", "8000"
	HasSection bool
}

// ExtractDockerfileExpectationFromArchitecture parses the ## Docker & Deployment section
// in architecture.md and returns the images, port, and command the Dockerfile must use.
func ExtractDockerfileExpectationFromArchitecture(archDoc string) DockerfileExpectation {
	var exp DockerfileExpectation
	loc := dockerDeploymentHeadingRE.FindStringIndex(archDoc)
	if loc == nil {
		return exp
	}
	exp.HasSection = true
	section := extractMarkdownSection(archDoc, loc[0])

	// Extract FROM images from code fences or inline FROM lines.
	fromRE := regexp.MustCompile(`(?im)^FROM\s+(\S+)`)
	for _, m := range fromRE.FindAllStringSubmatch(section, -1) {
		exp.BaseImages = append(exp.BaseImages, m[1])
	}

	// Extract EXPOSE port.
	exposeRE := regexp.MustCompile(`(?im)^EXPOSE\s+(\d+)`)
	if m := exposeRE.FindStringSubmatch(section); len(m) >= 2 {
		exp.ExposePort = m[1]
	}

	// Extract CMD array elements from CMD ["a", "b"] or CMD a b.
	cmdArrayRE := regexp.MustCompile(`CMD\s*\[\s*([^\]]+)\s*\]`)
	if m := cmdArrayRE.FindStringSubmatch(section); len(m) >= 2 {
		inner := m[1]
		for _, part := range regexp.MustCompile(`"([^"]+)"`).FindAllStringSubmatch(inner, -1) {
			if len(part) >= 2 {
				exp.CmdParts = append(exp.CmdParts, part[1])
			}
		}
	}
	if len(exp.CmdParts) == 0 {
		cmdShellRE := regexp.MustCompile(`(?im)^CMD\s+(.+)$`)
		if m := cmdShellRE.FindStringSubmatch(section); len(m) >= 2 {
			exp.CmdParts = strings.Fields(m[1])
		}
	}

	return exp
}

// ExtractDockerfileSnippetFromArchitecture returns the Dockerfile code block from the
// ## Docker & Deployment section of architecture.md, if present.
func ExtractDockerfileSnippetFromArchitecture(archDoc string) string {
	loc := dockerDeploymentHeadingRE.FindStringIndex(archDoc)
	if loc == nil {
		return ""
	}
	section := extractMarkdownSection(archDoc, loc[0])
	fenceRE := regexp.MustCompile("(?s)```(?:dockerfile|docker)?\\n(.*?)\\n```")
	if m := fenceRE.FindStringSubmatch(section); len(m) >= 2 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

// FormatDockerfileBeadContext returns the exact Dockerfile snippet from architecture.md
// for the polecat to use as a template when implementing the Dockerfile bead.
func FormatDockerfileBeadContext(rigDir, beadPath string, v WorkflowValidation) string {
	if !IsMainDockerfile(beadPath, v.LayoutRoot) {
		return ""
	}
	archDoc := readRigDoc(rigDir, "architecture.md")
	snippet := ExtractDockerfileSnippetFromArchitecture(archDoc)
	if snippet == "" {
		return ""
	}
	return strings.TrimSpace("### Dockerfile template (from architecture.md)\n" +
		"Use this as the starting point for `" + beadPath + "`. You may adapt paths, but the base images, port, and CMD must match.\n" +
		"```dockerfile\n" + snippet + "\n```")
}

// ValidateDockerfileAgainstArchitecture rejects Dockerfile content that does not match
// the images, port, and command documented in architecture.md.
func ValidateDockerfileAgainstArchitecture(dockerfileContent, archDoc, relPath string) error {
	exp := ExtractDockerfileExpectationFromArchitecture(archDoc)
	if !exp.HasSection {
		return nil
	}
	lower := strings.ToLower(dockerfileContent)

	for _, img := range exp.BaseImages {
		if !strings.Contains(lower, strings.ToLower(img)) {
			return fmt.Errorf("%s must use base image %s documented in architecture.md ## Docker & Deployment", relPath, img)
		}
	}
	if exp.ExposePort != "" && !strings.Contains(lower, "expose "+exp.ExposePort) {
		return fmt.Errorf("%s must EXPOSE port %s per architecture.md", relPath, exp.ExposePort)
	}
	if len(exp.CmdParts) > 0 {
		missing := false
		for _, part := range exp.CmdParts {
			if !strings.Contains(dockerfileContent, part) {
				missing = true
				break
			}
		}
		if missing {
			return fmt.Errorf("%s CMD must include %v per architecture.md ## Docker & Deployment", relPath, exp.CmdParts)
		}
	}
	return nil
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
// rig is the rig directory name (optional) — used to auto-correct spec-index confusion
// when layout_root equals rig name on flat mayor/rig worktrees.
func SanitizeRigFlowProfile(v WorkflowValidation, rig ...string) WorkflowValidation {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" {
		v.LayoutRoot = "."
		layout = "."
	}
	rigName := ""
	if len(rig) > 0 {
		rigName = rig[0]
	}
	// Auto-correct spec-index bug: rig name confused with layout_root on flat mayor/rig worktrees.
	// If all required_files share a common prefix matching the rig name, the project is at rig root.
	rigPrefix := commonPathPrefix(v.UnionRequiredFiles())
	if rigPrefix != "" && rigPrefix != "." && layout == rigPrefix && rigPrefix == rigName {
		// Strip the rig prefix from all file paths BEFORE setting layout to "."
		v.RequiredFiles = stripLayoutPrefixFromPaths(v.RequiredFiles, rigPrefix)
		for i := range v.DeliveryPhases {
			v.DeliveryPhases[i].RequiredFiles = stripLayoutPrefixFromPaths(v.DeliveryPhases[i].RequiredFiles, rigPrefix)
		}
		// Fix qa_verify_command: replace "cd <rigPrefix>" with "cd .", or add "cd . &&" if missing
		if q := strings.TrimSpace(v.QAVerifyCommand); q != "" {
			lower := strings.ToLower(q)
			if strings.HasPrefix(lower, "cd "+rigPrefix) {
				v.QAVerifyCommand = strings.Replace(q, "cd "+rigPrefix, "cd .", 1)
			} else if !strings.Contains(lower, "cd .") && !strings.Contains(lower, "cd ") {
				v.QAVerifyCommand = "cd . && " + q
			}
		}
		for i := range v.DeliveryPhases {
			if q := strings.TrimSpace(v.DeliveryPhases[i].QAVerifyCommand); q != "" {
				lower := strings.ToLower(q)
				if strings.HasPrefix(lower, "cd "+rigPrefix) {
					v.DeliveryPhases[i].QAVerifyCommand = strings.Replace(q, "cd "+rigPrefix, "cd .", 1)
				} else if !strings.Contains(lower, "cd .") && !strings.Contains(lower, "cd ") {
					v.DeliveryPhases[i].QAVerifyCommand = "cd . && " + q
				}
			}
		}
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
	healPhaseVerifyTestFiles(&v)
	healPhaseComposeFile(&v)
	// LLM judges occasionally emit malformed shell like "go test ./... && && (…)".
	// Collapse doubled operators and trim trailing separators on every phase
	// verify so a syntactically-broken command can never gate a phase.
	for i := range v.DeliveryPhases {
		if q := v.DeliveryPhases[i].QAVerifyCommand; q != "" {
			if fixed := sanitizeShellOperators(q); fixed != q {
				v.DeliveryPhases[i].QAVerifyCommand = fixed
				log.Printf("[profile-heal] phase %q: repaired malformed shell operators in verify", v.DeliveryPhases[i].ID)
			}
		}
	}
	// Normalize LLM-authored filename casing (e.g. "linkshelf/dockerfile" ->
	// "linkshelf/Dockerfile"): a wrong-case required_file makes Polecat write a
	// file the build can't find, deadlocking the bead on verify-before-close.
	canon := func(files []string) []string {
		out := make([]string, 0, len(files))
		for _, f := range files {
			nf := normalizeKnownFilenameCasing(filepath.ToSlash(strings.TrimSpace(f)))
			if nf != "" {
				out = append(out, nf)
			}
		}
		return out
	}
	v.RequiredFiles = canon(v.RequiredFiles)
	for i := range v.DeliveryPhases {
		v.DeliveryPhases[i].RequiredFiles = canon(v.DeliveryPhases[i].RequiredFiles)
	}
	// Docker builds inside a 300s CMD window cannot survive --no-cache on a
	// cold base image; layer reuse makes the same verify finish in minutes.
	for i := range v.DeliveryPhases {
		if q := v.DeliveryPhases[i].QAVerifyCommand; strings.Contains(q, "--no-cache") {
			v.DeliveryPhases[i].QAVerifyCommand = strings.ReplaceAll(q, "--no-cache", "")
			log.Printf("[profile-heal] phase %q: dropped --no-cache from docker verify (CMD timeout safety)", v.DeliveryPhases[i].ID)
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
	sanitizeFrontendOnlyPhaseQA(&v)
	return v
}

// sanitizeFrontendOnlyPhaseQA replaces npm test with typecheck-only in frontend-only
// delivery phases that have no unit-test files. Playwright/E2E tests belong in a
// dedicated e2e/deployment phase with a running server, not in the frontend build phase.
// healPhaseVerifyTestFiles fixes profile defects where a phase's
// qa_verify_command runs a test runner but the phase's required_files lack the
// test files that command actually executes — which plan_review/QA correctly
// reject as "verify requires file not in phase". Files are only pulled in when
// the command's scope actually covers them:
//   - "go test" with explicit package patterns covers only _test.go files under
//     those packages; bare "go test" covers all Go test files in the profile.
//   - bare "pytest"/"python -m pytest" covers all Python test files; explicit
//     .py arguments cover only those files.
//   - "playwright" pulls spec files plus the Playwright config.
func healPhaseVerifyTestFiles(v *WorkflowValidation) {
	union := v.UnionRequiredFiles()
	normalize := func(f string) string {
		return strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
	}
	goTestCovered := func(q, f string) bool {
		dir := strings.TrimSuffix(normalize(f), strings.ToLower(filepath.Base(f)))
		dir = strings.Trim(dir, "/")
		// Package patterns = non-flag tokens AFTER "go test", stopping at
		// shell operators ("cd x && go test ./pkg" must not treat cd/x as pkgs).
		fields := strings.Fields(q)
		var pkgs []string
		for i := 0; i+1 < len(fields); i++ {
			if fields[i] != "go" || fields[i+1] != "test" {
				continue
			}
			for _, tok := range fields[i+2:] {
				if tok == "&&" || tok == "||" || tok == ";" || tok == "|" {
					break
				}
				if strings.HasPrefix(tok, "-") {
					continue
				}
				pkgs = append(pkgs, strings.Trim(tok, "\"'"))
			}
			break
		}
		if len(pkgs) == 0 {
			return true // bare `go test` walks the whole module
		}
		for _, p := range pkgs {
			p = strings.Trim(strings.TrimPrefix(p, "./"), "/")
			if p == "" || p == "." {
				return true
			}
			if strings.HasPrefix(dir, p) {
				return true
			}
		}
		return false
	}
	pytestCovered := func(q, f string) bool {
		base := strings.ToLower(filepath.Base(f))
		for _, tok := range strings.Fields(q) {
			if strings.HasSuffix(tok, ".py") && strings.EqualFold(filepath.Base(strings.Trim(tok, "\"'")), base) {
				return true // explicitly named
			}
		}
		// Bare pytest collects test_*.py / *_test.py from the rootdir.
		return !strings.Contains(q, ".py") ||
			strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")
	}

	for i := range v.DeliveryPhases {
		raw := strings.TrimSpace(v.DeliveryPhases[i].QAVerifyCommand)
		q := strings.ToLower(raw)
		if q == "" {
			continue
		}
		has := map[string]bool{}
		for _, f := range v.DeliveryPhases[i].RequiredFiles {
			has[normalize(filepath.Base(f))] = true
		}
		addIf := func(token string, matchBase func(string) bool, covered func(string, string) bool) {
			if !strings.Contains(q, token) {
				return
			}
			for _, f := range union {
				b := normalize(filepath.Base(f))
				if has[b] || !matchBase(b) || !covered(q, f) {
					continue
				}
				v.DeliveryPhases[i].RequiredFiles = append(v.DeliveryPhases[i].RequiredFiles, filepath.ToSlash(strings.TrimSpace(f)))
				has[b] = true
				log.Printf("[profile-heal] phase %q: qa_verify_command %q executes %s — added to required_files", v.DeliveryPhases[i].ID, raw, f)
			}
		}
		addIf("go test",
			func(b string) bool { return strings.HasSuffix(b, "_test.go") },
			goTestCovered)
		addIf("pytest",
			func(b string) bool {
				return strings.HasSuffix(b, ".py") && (strings.HasPrefix(b, "test_") || strings.HasSuffix(b, "_test.py"))
			},
			pytestCovered)
		addIf("playwright",
			func(b string) bool {
				return strings.Contains(b, ".spec.") || b == "playwright.config.ts" || b == "playwright.config.js"
			},
			func(q, f string) bool { return true })
	}
}

// healPhaseComposeFile fixes profile defects where a phase's qa_verify_command
// invokes docker-compose but the phase's required_files lack the compose file
// itself — nothing would ever create it, so verify fails with "no configuration
// file provided" forever. The compose file lives at the layout root (the same
// directory the verify command cd's into).
func healPhaseComposeFile(v *WorkflowValidation) {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	composePath := "docker-compose.yml"
	if layout != "" && layout != "." {
		composePath = layout + "/docker-compose.yml"
	}
	for i := range v.DeliveryPhases {
		q := strings.ToLower(strings.TrimSpace(v.DeliveryPhases[i].QAVerifyCommand))
		if !strings.Contains(q, "docker-compose") && !strings.Contains(q, "docker compose") {
			continue
		}
		found := false
		for _, f := range v.DeliveryPhases[i].RequiredFiles {
			if strings.EqualFold(filepath.ToSlash(strings.TrimSpace(f)), composePath) {
				found = true
				break
			}
		}
		if !found {
			v.DeliveryPhases[i].RequiredFiles = append(v.DeliveryPhases[i].RequiredFiles, composePath)
			log.Printf("[profile-heal] phase %q: qa_verify_command runs docker-compose — added %s to required_files", v.DeliveryPhases[i].ID, composePath)
		}
	}
}

// sanitizeShellOperators repairs LLM-authored shell: doubled operators
// ("&& &&"), leading operators, and trailing separators.
func sanitizeShellOperators(cmd string) string {
	out := cmd
	for {
		next := out
		next = strings.ReplaceAll(next, "&& &&", "&&")
		next = strings.ReplaceAll(next, "|| ||", "||")
		next = strings.ReplaceAll(next, "; ;", ";")
		if next == out {
			break
		}
		out = next
	}
	out = strings.TrimSpace(out)
	out = strings.TrimLeft(out, "&|; ")
	return strings.TrimSpace(out)
}

// canonicalFileBasenames maps case-insensitive basenames to their canonical
// on-disk spelling. Build tooling (Docker's build: ., make, Go) expects the
// exact casing; an LLM-authored profile that says "dockerfile" makes Polecat
// write a file the build then cannot find.
var canonicalFileBasenames = map[string]string{
	"dockerfile":         "Dockerfile",
	"makefile":           "Makefile",
	"docker-compose.yml": "docker-compose.yml",
	".gitignore":         ".gitignore",
}

// normalizeKnownFilenameCasing fixes casing for basenames in
// canonicalFileBasenames and returns the cleaned, slash-normalized path.
func normalizeKnownFilenameCasing(f string) string {
	if f == "" {
		return ""
	}
	f = filepath.ToSlash(f)
	base := strings.ToLower(filepath.Base(f))
	if canon, ok := canonicalFileBasenames[base]; ok {
		return filepath.Join(filepath.Dir(f), canon)
	}
	return f
}

func sanitizeFrontendOnlyPhaseQA(v *WorkflowValidation) {
	for i := range v.DeliveryPhases {
		p := &v.DeliveryPhases[i]
		frontendDir := frontendPhaseRoot(p.RequiredFiles)
		if frontendDir == "" {
			continue
		}
		if hasFrontendUnitTests(p.RequiredFiles) {
			continue
		}
		p.QAVerifyCommand = sanitizeFrontendQACommand(p.QAVerifyCommand, frontendDir)
	}
}

// frontendPhaseRoot returns the common frontend directory (e.g. "frontend") when
// all required files live under it and look like Node/TS files, otherwise "".
func frontendPhaseRoot(files []string) string {
	var dir string
	var hasNodeFile bool
	for _, f := range files {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		if !looksLikeNodeFile(f) {
			continue
		}
		hasNodeFile = true
		idx := strings.Index(f, "/")
		if idx <= 0 {
			return ""
		}
		first := f[:idx]
		if dir == "" {
			dir = first
		} else if dir != first {
			return ""
		}
	}
	if !hasNodeFile || dir == "" {
		return ""
	}
	return dir
}

// hasFrontendUnitTests reports whether the required files include frontend unit
// test files (e.g. *.test.tsx, *.test.ts) — distinct from Playwright E2E specs.
func hasFrontendUnitTests(files []string) bool {
	for _, f := range files {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		if strings.Contains(lower, ".test.") || strings.Contains(lower, ".spec.") {
			// Playwright E2E specs live in a dedicated test/ directory and are not
			// frontend unit tests.
			if strings.HasPrefix(lower, "test/") || strings.Contains(lower, "/test/") {
				continue
			}
			return true
		}
	}
	return false
}

// sanitizeFrontendQACommand rewrites a frontend QA verify command so it typechecks
// instead of running E2E tests that require a live server.
func sanitizeFrontendQACommand(cmd, frontendDir string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return cmd
	}
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "npm") && !strings.Contains(lower, "yarn") && !strings.Contains(lower, "pnpm") {
		return cmd
	}
	// Replace npm/yarn/pnpm test with typecheck (case-insensitive).
	testRE := regexp.MustCompile(`(?i)\b(npm|yarn|pnpm)\s+test\b`)
	cmd = testRE.ReplaceAllString(cmd, "npx tsc --noEmit")
	// Deduplicate consecutive typecheck runs if the command already had tsc.
	cmd = strings.ReplaceAll(cmd, "npx tsc --noEmit && npx tsc --noEmit", "npx tsc --noEmit")
	// Harden any dependency install against lifecycle-script supply-chain worms.
	cmd = HardenNodeInstallCommand(cmd)
	// Ensure the command cds to the frontend directory.
	if !strings.Contains(strings.ToLower(cmd), "cd "+frontendDir) {
		cmd = "cd " + frontendDir + " && " + cmd
	}
	return cmd
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
	// If architecture uses "layout_root/" as a placeholder (not the actual directory name),
	// don't strip the prefix — the SPEC tree is authoritative.
	for _, p := range archPaths {
		if strings.HasPrefix(p, "layout_root/") {
			return v
		}
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
			stripped := stripLayoutFromVerifyCmd(q, layout)
			if stripped != q {
				log.Printf("[align-layout] phase %q verify cmd stripped: %q → %q", v.DeliveryPhases[i].ID, q, stripped)
			}
			v.DeliveryPhases[i].QAVerifyCommand = dockerVerifyWithLayout(stripped, ".")
		}
	}
	if q := strings.TrimSpace(v.QAVerifyCommand); q != "" {
		stripped := stripLayoutFromVerifyCmd(q, layout)
		if stripped != q {
			log.Printf("[align-layout] top-level QA verify cmd stripped: %q → %q", q, stripped)
		}
		v.QAVerifyCommand = dockerVerifyWithLayout(stripped, ".")
	}
	return v
}

// stripLayoutFromVerifyCmd removes the rig name prefix from paths in a verify
// command. The LLM sometimes emits paths like "finally/backend/main.py" or
// "cd finally/backend" where the rig name (e.g. "finally") is not part of the
// actual project directory structure.
//
// Handles:
//   - cd <layout>/<subdir> → cd <subdir>
//   - cd <layout> → stripped entirely (bare cd to rig root is pointless)
//   - test -f <layout>/<path> → test -f <path>
//   - bare <layout>/<path> → <path>
func stripLayoutFromVerifyCmd(cmd, layout string) string {
	if layout == "" || layout == "." {
		return cmd
	}
	layoutSlash := layout + "/"

	// 1. Strip leading "cd <layout>/<subdir>" → "cd <subdir>"
	re := regexp.MustCompile(`\bcd\s+` + regexp.QuoteMeta(layout) + `/`)
	cmd = re.ReplaceAllString(cmd, "cd ")

	// 2. Strip bare "cd <layout>" (no subdirectory, just the rig name)
	re2 := regexp.MustCompile(`\bcd\s+` + regexp.QuoteMeta(layout) + `(\s|$)`)
	cmd = re2.ReplaceAllString(cmd, "")

	// 3. Strip "test -f <layout>/<path>" → "test -f <path>"
	re3 := regexp.MustCompile(`(\btest\s+-f\s+)` + regexp.QuoteMeta(layout) + `/`)
	cmd = re3.ReplaceAllString(cmd, `${1}`)

	// 4. Strip bare "<layout>/" when it's prefixed to a path argument
	cmd = strings.ReplaceAll(cmd, layoutSlash, "")

	// Clean up: remove empty "&&" segments, double spaces, leading/trailing junk
	cmd = strings.TrimSpace(cmd)
	cmd = strings.TrimPrefix(cmd, "&& ")
	cmd = strings.TrimSpace(cmd)
	cmd = strings.TrimSuffix(cmd, "&&")
	cmd = strings.TrimSpace(cmd)

	return cmd
}

func DoubledLayoutPath(path, layoutRoot string) bool {
	layoutRoot = strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	path = filepath.ToSlash(strings.TrimSpace(path))
	if layoutRoot == "" || path == "" {
		return false
	}
	return strings.Contains(path, layoutRoot+"/"+layoutRoot+"/")
}
