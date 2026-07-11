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

func TestDoubledLayoutPath(t *testing.T) {
	if !DoubledLayoutPath("finally/finally/Dockerfile", "finally") {
		t.Fatal("expected doubled path detection")
	}
	if DoubledLayoutPath("finally/Dockerfile", "finally") {
		t.Fatal("valid path should not match")
	}
}
