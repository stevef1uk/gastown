package orchestrator

import (
	"strings"
	"testing"
)

func TestPhaseShipsDockerPlaywright_contentDriven(t *testing.T) {
	cases := []struct {
		name  string
		phase DeliveryPhase
		want  bool
	}{
		{
			name: "test phase named test with compose+playwright ships",
			phase: DeliveryPhase{
				ID:    "test",
				Title: "Testing & Release",
				RequiredFiles: []string{
					"finally/docker-compose.yml",
					"finally/test/docker-compose.test.yml",
					"finally/test/playwright.config.ts",
					"finally/test/e2e.spec.ts",
				},
			},
			want: true,
		},
		{
			name: "production phase with compose but no playwright does not ship",
			phase: DeliveryPhase{
				ID:            "project-foundation",
				Title:        "Project Foundation",
				RequiredFiles: []string{"finally/docker-compose.yml", "finally/Dockerfile", "finally/scripts/start.sh"},
			},
			want: false,
		},
		{
			name: "phase with playwright but no compose does not ship",
			phase: DeliveryPhase{
				ID:            "frontend",
				Title:        "Frontend",
				RequiredFiles: []string{"finally/frontend/package.json", "finally/test/playwright.config.ts"},
			},
			want: false,
		},
		{
			name: "nil phase does not ship",
			phase: DeliveryPhase{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var p *DeliveryPhase
			if tc.phase.ID != "" || tc.phase.Title != "" || len(tc.phase.RequiredFiles) > 0 {
				pp := tc.phase
				p = &pp
			}
			if got := phaseShipsDockerPlaywright(p); got != tc.want {
				t.Fatalf("phaseShipsDockerPlaywright(%q) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestDockerComposeFileForPhase_prefersTestHarness(t *testing.T) {
	cases := []struct {
		name  string
		files []string
		want  string
	}{
		{
			name: "test harness preferred over production compose",
			files: []string{
				"finally/docker-compose.yml",
				"finally/test/docker-compose.test.yml",
			},
			want: "finally/test/docker-compose.test.yml",
		},
		{
			name: "only production compose falls back to it",
			files: []string{
				"finally/docker-compose.yml",
				"finally/Dockerfile",
			},
			want: "finally/docker-compose.yml",
		},
		{
			name:  "no compose files returns empty",
			files: []string{"finally/backend/pyproject.toml"},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dockerComposeFileForPhase(tc.files); got != tc.want {
				t.Fatalf("dockerComposeFileForPhase(%v) = %q, want %q", tc.files, got, tc.want)
			}
		})
	}
}

func TestComposePlaywrightVerifyCommand_usesTestHarness(t *testing.T) {
	prev := dockerComposeCLIOverride
	dockerComposeCLIOverride = "docker-compose"
	t.Cleanup(func() { dockerComposeCLIOverride = prev })

	p := &DeliveryPhase{
		RequiredFiles: []string{
			"finally/docker-compose.yml",
			"finally/test/docker-compose.test.yml",
			"finally/test/e2e.spec.ts",
		},
	}
	cmd := composePlaywrightVerifyCommand(p, "finally")
	for _, want := range []string{
		"cd finally",
		"docker-compose -f test/docker-compose.test.yml down",
		"docker-compose -f test/docker-compose.test.yml up --exit-code-from playwright",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("compose command %q missing %q", cmd, want)
		}
	}
	if strings.Contains(cmd, "-f docker-compose.yml") {
		t.Fatalf("compose command must not target production compose file: %q", cmd)
	}
}
