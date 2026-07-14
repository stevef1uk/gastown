package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowUsesDocker_finallyProfile(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:      "finally",
		TestRunner:      "custom",
		QAVerifyCommand: "docker build .",
		RequiredFiles:   []string{"finally/Dockerfile"},
	}
	if !WorkflowUsesDocker(v) {
		t.Fatal("expected docker workflow")
	}
	if WorkflowUsesGo(v) || WorkflowUsesPython(v) {
		t.Fatal("docker profile must not be classified as go/python")
	}
}

func TestAdaptDockerComposeCommand(t *testing.T) {
	t.Parallel()
	prev := dockerComposeCLIOverride
	t.Cleanup(func() { dockerComposeCLIOverride = prev })

	dockerComposeCLIOverride = "docker-compose"
	if got := AdaptDockerComposeCommand("docker compose -f docker-compose.yml config"); got != "docker-compose -f docker-compose.yml config" {
		t.Fatalf("v1 host: got %q", got)
	}
	dockerComposeCLIOverride = "docker compose"
	if got := AdaptDockerComposeCommand("docker-compose -f test/docker-compose.test.yml up"); got != "docker compose -f test/docker-compose.test.yml up" {
		t.Fatalf("v2 host: got %q", got)
	}
}

func TestDockerImplementationVerifyCommandForBead_compose(t *testing.T) {
	prev := dockerComposeCLIOverride
	t.Cleanup(func() { dockerComposeCLIOverride = prev })
	dockerComposeCLIOverride = "docker-compose"

	v := WorkflowValidation{LayoutRoot: "finally"}
	got := DockerImplementationVerifyCommandForBead(v, "/tmp/rig", "finally/docker-compose.yml")
	want := "cd finally && docker-compose -f docker-compose.yml config"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	flat := WorkflowValidation{LayoutRoot: "."}
	got = DockerImplementationVerifyCommandForBead(flat, "/tmp/rig", "docker-compose.yml")
	want = "docker-compose -f docker-compose.yml config"
	if got != want {
		t.Fatalf("flat layout got %q want %q", got, want)
	}
}

func TestDockerImplementationVerifyCommandForBead(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:      "finally",
		QAVerifyCommand: "docker build .",
		DeliveryPhases: []DeliveryPhase{{
			ID:              "setup",
			QAVerifyCommand: "docker build .",
			RequiredFiles:   []string{"finally/Dockerfile"},
		}},
		ActivePhaseIDField: "setup",
	}
	scoped := v.ForActivePhase()
	got := DockerImplementationVerifyCommandForBead(scoped, "/tmp/rig", "finally/Dockerfile")
	want := "cd finally && docker build -f Dockerfile ."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestDockerImplementationVerifyCommandForBead_testCompose(t *testing.T) {
	prev := dockerComposeCLIOverride
	t.Cleanup(func() { dockerComposeCLIOverride = prev })
	dockerComposeCLIOverride = "docker-compose"

	flat := WorkflowValidation{LayoutRoot: "."}
	got := DockerImplementationVerifyCommandForBead(flat, "/tmp/rig", "test/docker-compose.test.yml")
	want := "docker-compose -f test/docker-compose.test.yml config"
	if got != want {
		t.Fatalf("test compose got %q want %q", got, want)
	}
}

func TestDockerImplementationVerifyCommandForBead_shellScript(t *testing.T) {
	prev := dockerComposeCLIOverride
	t.Cleanup(func() { dockerComposeCLIOverride = prev })
	dockerComposeCLIOverride = "docker-compose"

	flat := WorkflowValidation{LayoutRoot: "."}
	got := DockerImplementationVerifyCommandForBead(flat, "/tmp/rig", "scripts/start_mac.sh")
	want := "bash -n scripts/start_mac.sh"
	if got != want {
		t.Fatalf("shell script got %q want %q", got, want)
	}
}

func TestDockerImplementationVerifyCommandForBead_e2eSpec(t *testing.T) {
	prev := dockerComposeCLIOverride
	t.Cleanup(func() { dockerComposeCLIOverride = prev })
	dockerComposeCLIOverride = "docker compose"

	v := WorkflowValidation{
		LayoutRoot:      ".",
		QAVerifyCommand: "docker compose -f test/docker-compose.test.yml up --exit-code-from playwright",
	}
	got := DockerImplementationVerifyCommandForBead(v, "/tmp/rig", "test/e2e/trading_flow.spec.ts")
	want := "docker compose -f test/docker-compose.test.yml up --exit-code-from playwright"
	if got != want {
		t.Fatalf("e2e spec got %q want %q", got, want)
	}
}

func TestNormalizeDockerCommand_buildDotTypo(t *testing.T) {
	in := "cd finally/mayor/rig && docker build."
	want := "cd finally/mayor/rig && docker build ."
	if got := NormalizeDockerCommand(in); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeRigFlowProfile_finally(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:      "finally",
		TestRunner:      "custom",
		QAVerifyCommand: "docker build .",
		RequiredFiles: []string{
			"finally/Dockerfile",
			"finally/planning/PLAN.md",
			"finally/finally/Dockerfile",
		},
		DeliveryPhases: []DeliveryPhase{{
			ID:              "setup",
			QAVerifyCommand: "docker build .",
			RequiredFiles:   []string{"finally/Dockerfile", "finally/planning/PLAN.md"},
		}},
	}
	got := SanitizeRigFlowProfile(v)
	for _, bad := range []string{"planning/PLAN.md", "finally/finally/"} {
		for _, f := range got.RequiredFiles {
			if strings.Contains(f, bad) {
				t.Fatalf("required_files still contains %q: %v", bad, got.RequiredFiles)
			}
		}
	}
	if !strings.Contains(got.QAVerifyCommand, "cd finally &&") {
		t.Fatalf("qa_verify = %q", got.QAVerifyCommand)
	}
}

func TestAlignProfileLayoutWithArchitecture_finallyFlatArch(t *testing.T) {
	arch := filepath.Join(t.TempDir(), "architecture.md")
	body := "# Arch\n- **`backend/main.py`**  \n- **`Dockerfile`**  \n- **`docker-compose.yml`**  \n"
	if err := os.WriteFile(arch, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	v := WorkflowValidation{
		LayoutRoot:        "finally",
		BeadTitleContains: "Implement finally/",
		RequiredFiles:     []string{"finally/Dockerfile", "finally/backend/main.py"},
		DeliveryPhases: []DeliveryPhase{{
			ID:            "setup",
			RequiredFiles: []string{"finally/Dockerfile", "finally/docker-compose.yml"},
		}},
		ActivePhaseIDField: "setup",
	}
	got := AlignProfileLayoutWithArchitecture(v, arch)
	if got.LayoutRoot != "." {
		t.Fatalf("layout_root = %q want .", got.LayoutRoot)
	}
	if got.BeadTitleContains != "Implement " {
		t.Fatalf("bead prefix = %q", got.BeadTitleContains)
	}
	if got.RequiredFiles[0] != "Dockerfile" {
		t.Fatalf("required_files = %v", got.RequiredFiles)
	}
}

func TestSanitizeRigFlowProfile_flatRepoBeadPrefix(t *testing.T) {
	got := SanitizeRigFlowProfile(WorkflowValidation{
		LayoutRoot:        ".",
		BeadTitleContains: "Implement finally/",
		TestRunner:        "custom",
		QAVerifyCommand:   "docker compose up",
	})
	if got.BeadTitleContains != "Implement " {
		t.Fatalf("bead_title_contains = %q want Implement ", got.BeadTitleContains)
	}
}

func TestSanitizeRigFlowProfile_preservesLayoutSubdirPrefix(t *testing.T) {
	got := SanitizeRigFlowProfile(WorkflowValidation{
		LayoutRoot:        "api",
		BeadTitleContains: "Implement api/",
		QAVerifyCommand:   "pytest -q",
	})
	if got.BeadTitleContains != "Implement api/" {
		t.Fatalf("bead_title_contains = %q want Implement api/", got.BeadTitleContains)
	}
}

func TestSanitizeRigFlowProfile_frontendPhaseTypecheckOnly(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:        ".",
		BeadTitleContains: "Implement finally/",
		DeliveryPhases: []DeliveryPhase{{
			ID:              "frontend-ui",
			Title:           "Frontend",
			RequiredFiles:   []string{"frontend/package.json", "frontend/app/page.tsx"},
			QAVerifyCommand: "cd frontend && npm install && npx tsc --noEmit && npm test",
		}},
	}
	got := SanitizeRigFlowProfile(v)
	q := got.DeliveryPhases[0].QAVerifyCommand
	if strings.Contains(strings.ToLower(q), "npm test") {
		t.Fatalf("frontend QA should not run npm test, got %q", q)
	}
	if !strings.Contains(q, "npx tsc --noEmit") {
		t.Fatalf("frontend QA should typecheck, got %q", q)
	}
}

func TestSanitizeRigFlowProfile_frontendPhaseKeepsUnitTests(t *testing.T) {
	v := WorkflowValidation{
		LayoutRoot:        ".",
		BeadTitleContains: "Implement finally/",
		DeliveryPhases: []DeliveryPhase{{
			ID:              "frontend-ui",
			Title:           "Frontend",
			RequiredFiles:   []string{"frontend/package.json", "frontend/components/Widget.test.tsx"},
			QAVerifyCommand: "cd frontend && npm install && npm test",
		}},
	}
	got := SanitizeRigFlowProfile(v)
	q := got.DeliveryPhases[0].QAVerifyCommand
	if !strings.Contains(strings.ToLower(q), "npm test") {
		t.Fatalf("frontend QA with unit tests should keep npm test, got %q", q)
	}
}

func TestDockerVerifyWithLayout_flatRepoNoBrokenCd(t *testing.T) {
	prev := dockerComposeCLIOverride
	t.Cleanup(func() { dockerComposeCLIOverride = prev })
	dockerComposeCLIOverride = "docker-compose"

	in := "cd  && docker-compose -f test/docker-compose.test.yml up --abort-on-container-exit"
	got := dockerVerifyWithLayout(in, "")
	want := "docker-compose -f test/docker-compose.test.yml up --abort-on-container-exit"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if strings.Contains(got, "\\u0026") || strings.Contains(got, "cd  &&") {
		t.Fatalf("unexpected escapes or broken cd: %q", got)
	}
}

func TestExtractDockerfileSnippetFromArchitecture(t *testing.T) {
	arch := "# Architecture\n\n## Docker & Deployment\n\n```dockerfile\nFROM node:20-slim\nRUN npm run build\nFROM python:3.12-slim\nCMD [\"uvicorn\", \"backend.main:app\"]\n```\n"
	got := ExtractDockerfileSnippetFromArchitecture(arch)
	want := "FROM node:20-slim\nRUN npm run build\nFROM python:3.12-slim\nCMD [\"uvicorn\", \"backend.main:app\"]"
	if got != want {
		t.Fatalf("snippet mismatch:\nGot:\n%s\n\nWant:\n%s", got, want)
	}
}

func TestFormatDockerfileBeadContext(t *testing.T) {
	dir := t.TempDir()
	rigDir := filepath.Join(dir, "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	arch := "# Architecture\n\n## Docker & Deployment\n\n```dockerfile\nFROM node:20-slim\nFROM python:3.12-slim\nEXPOSE 8000\nCMD [\"uvicorn\", \"backend.main:app\"]\n```\n"
	if err := os.WriteFile(filepath.Join(rigDir, "architecture.md"), []byte(arch), 0644); err != nil {
		t.Fatal(err)
	}
	got := FormatDockerfileBeadContext(rigDir, "Dockerfile", WorkflowValidation{LayoutRoot: "."})
	if !strings.Contains(got, "FROM node:20-slim") || !strings.Contains(got, "FROM python:3.12-slim") {
		t.Fatalf("context missing expected images:\n%s", got)
	}
}

func TestExtractDockerfileExpectationFromArchitecture(t *testing.T) {
	arch := "# Architecture\n\n## Docker & Deployment\n\n```dockerfile\nFROM node:20-slim AS builder\nRUN npm run build\nFROM python:3.12-slim\nEXPOSE 8000\nCMD [\"uvicorn\", \"backend.main:app\", \"--host\", \"0.0.0.0\", \"--port\", \"8000\"]\n```\n"
	exp := ExtractDockerfileExpectationFromArchitecture(arch)
	if !exp.HasSection {
		t.Fatal("expected section found")
	}
	wantImgs := []string{"node:20-slim", "python:3.12-slim"}
	if len(exp.BaseImages) != len(wantImgs) {
		t.Fatalf("images = %v, want %v", exp.BaseImages, wantImgs)
	}
	if exp.ExposePort != "8000" {
		t.Fatalf("port = %q, want 8000", exp.ExposePort)
	}
	if len(exp.CmdParts) == 0 || exp.CmdParts[0] != "uvicorn" {
		t.Fatalf("cmd = %v", exp.CmdParts)
	}
}

func TestValidateDockerfileAgainstArchitecture(t *testing.T) {
	arch := "# Architecture\n\n## Docker & Deployment\n\n```dockerfile\nFROM node:20-slim AS builder\nFROM python:3.12-slim\nEXPOSE 8000\nCMD [\"uvicorn\", \"backend.main:app\", \"--host\", \"0.0.0.0\", \"--port\", \"8000\"]\n```\n"

	good := "FROM node:20-slim\nFROM python:3.12-slim\nEXPOSE 8000\nCMD [\"uvicorn\", \"backend.main:app\", \"--host\", \"0.0.0.0\", \"--port\", \"8000\"]\n"
	if err := ValidateDockerfileAgainstArchitecture(good, arch, "Dockerfile"); err != nil {
		t.Fatalf("expected good Dockerfile to pass: %v", err)
	}

	bad := "FROM python:3.11-slim\nEXPOSE 5000\nCMD [\"python\", \"-m\", \"flask\", \"run\"]\n"
	if err := ValidateDockerfileAgainstArchitecture(bad, arch, "Dockerfile"); err == nil {
		t.Fatal("expected bad Dockerfile to fail")
	}
}

func TestIsMainDockerfile(t *testing.T) {
	if !IsMainDockerfile("Dockerfile", ".") {
		t.Fatal("Dockerfile at root should be main")
	}
	if !IsMainDockerfile("finally/Dockerfile", "finally") {
		t.Fatal("Dockerfile under layout root should be main")
	}
	if IsMainDockerfile("test/Dockerfile", ".") {
		t.Fatal("test/Dockerfile should not be main")
	}
}

func TestDoubledLayoutPath(t *testing.T) {
	if !DoubledLayoutPath("finally/finally/Dockerfile", "finally") {
		t.Fatal("expected doubled path detection")
	}
	if DoubledLayoutPath("finally/Dockerfile", "finally") {
		t.Fatal("valid path should not match")
	}
}
