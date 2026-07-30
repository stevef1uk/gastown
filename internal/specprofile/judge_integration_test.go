// +build integration

package specprofile

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestJudgePhaseVerifyCommandsWithFreerideProxy(t *testing.T) {
	if os.Getenv("GASTOWN_TEST_FREERIDE") != "1" {
		t.Skip("Set GASTOWN_TEST_FREERIDE=1 to run Judge integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	pingCtx, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	defer pingCancel()
	if !pingFreerideProxy(pingCtx, t) {
		t.Skip("freeride proxy not reachable at localhost:11434")
	}

	tests := []struct {
		name                 string
		goodPhaseFiles       []string
		goodCmd              string
		badPhaseFiles        []string
		badCmd               string
		emptyPhaseFiles      []string
		emptyCmd             string
		checkGood            func(t *testing.T, cmd string)
		checkBad             func(t *testing.T, cmd string)
		checkEmpty           func(t *testing.T, cmd string)
	}{
		{
			name:           "uv-run-pytest-preserved",
			goodPhaseFiles: []string{"backend/tests/test_db.py", "backend/pyproject.toml"},
			goodCmd:        "cd backend && uv run pytest -v tests/test_db.py",
			badPhaseFiles:  []string{"scripts/start_mac.sh", "scripts/stop_mac.sh"},
			badCmd:         "echo 'verify ok'",
			emptyPhaseFiles: []string{"frontend/src/App.tsx", "frontend/package.json"},
			emptyCmd:       "",
			checkGood: func(t *testing.T, cmd string) {
				t.Helper()
				if cmd != "cd backend && uv run pytest -v tests/test_db.py" {
					t.Errorf("good command changed: %q", cmd)
				}
			},
			checkBad: func(t *testing.T, cmd string) {
				t.Helper()
				// Scripts phase needs test -f checks, not bare echo
				if !strings.Contains(cmd, "test -f") {
					t.Errorf("bad command missing test -f checks: %q", cmd)
				}
			},
			checkEmpty: func(t *testing.T, cmd string) {
				t.Helper()
				// Empty commands may or may not be filled depending on
				// validator approval — no hard assertion.
			},
		},
		{
			name:           "go-test-preserved",
			goodPhaseFiles: []string{"cmd/server/main.go", "go.mod", "go.sum"},
			goodCmd:        "cd . && go test ./...",
			badPhaseFiles:  []string{"scripts/start_mac.sh", "scripts/stop_mac.sh"},
			badCmd:         "cd frontend && npm test",
			emptyPhaseFiles: []string{"frontend/src/App.tsx", "frontend/package.json"},
			emptyCmd:       "echo 'placeholder'",
			checkGood: func(t *testing.T, cmd string) {
				t.Helper()
				if cmd != "cd . && go test ./..." {
					t.Errorf("good go command changed: %q", cmd)
				}
			},
			checkBad: func(t *testing.T, cmd string) {
				t.Helper()
				// May or may not be replaced depending on LLM/validator.
			},
			checkEmpty: func(t *testing.T, cmd string) {
				t.Helper()
				// May or may not be replaced depending on LLM/validator.
			},
		},
		{
			name:           "docker-phase-command-preserved",
			goodPhaseFiles: []string{"Dockerfile", "docker-compose.yml"},
			goodCmd:        "test -f Dockerfile && test -f docker-compose.yml && echo 'docker ok'",
			badPhaseFiles:  []string{"scripts/start_mac.sh", "scripts/stop_mac.sh"},
			badCmd:         "cd backend && python -m pytest -v",
			emptyPhaseFiles: []string{"frontend/src/App.tsx", "frontend/package.json"},
			emptyCmd:       "cd . && echo 'verify ok (no automated tests for this phase)'",
			checkGood: func(t *testing.T, cmd string) {
				t.Helper()
				if !strings.Contains(cmd, "docker") && !strings.Contains(cmd, "test -f") {
					t.Errorf("good docker command changed: %q", cmd)
				}
			},
			checkBad: func(t *testing.T, cmd string) {
				t.Helper()
				// May or may not be replaced depending on LLM/validator.
			},
			checkEmpty: func(t *testing.T, cmd string) {
				t.Helper()
				// May or may not be replaced depending on LLM/validator.
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := orchestrator.WorkflowValidation{
				DeliveryPhases: []orchestrator.DeliveryPhase{
					{
						ID:              "good-phase",
						Title:           "Good Phase",
						QAVerifyCommand: tc.goodCmd,
						RequiredFiles:   tc.goodPhaseFiles,
						SpecFocus:       "Test preservation",
					},
					{
						ID:              "bad-phase",
						Title:           "Bad Phase",
						QAVerifyCommand: tc.badCmd,
						RequiredFiles:   tc.badPhaseFiles,
						SpecFocus:       "Test replacement",
					},
					{
						ID:              "empty-phase",
						Title:           "Empty Phase",
						QAVerifyCommand: tc.emptyCmd,
						RequiredFiles:   tc.emptyPhaseFiles,
						SpecFocus:       "Test filling",
					},
					{
						ID:              "good-docker-phase",
						Title:           "Good Docker Phase",
						QAVerifyCommand: "test -f Dockerfile && test -f docker-compose.yml && echo 'docker ok'",
						RequiredFiles:   []string{"Dockerfile", "docker-compose.yml"},
						SpecFocus:       "Containerization",
					},
				},
			}

			v = JudgePhaseVerifyCommands(ctx, "http://localhost:11434/v1/chat/completions", "deepseek/deepseek-v4-flash", "http://localhost:11434/v1/chat/completions", "deepseek/deepseek-v4-flash", v)

			find := func(id string) *orchestrator.DeliveryPhase {
				for i := range v.DeliveryPhases {
					if v.DeliveryPhases[i].ID == id {
						return &v.DeliveryPhases[i]
					}
				}
				return nil
			}

			if p := find("good-phase"); p != nil {
				tc.checkGood(t, p.QAVerifyCommand)
			} else {
				t.Error("good-phase missing")
			}
			if p := find("bad-phase"); p != nil {
				tc.checkBad(t, p.QAVerifyCommand)
			} else {
				t.Error("bad-phase missing")
			}
			if p := find("empty-phase"); p != nil {
				tc.checkEmpty(t, p.QAVerifyCommand)
			} else {
				t.Error("empty-phase missing")
			}
			if p := find("good-docker-phase"); p != nil {
				cmd := p.QAVerifyCommand
				if !strings.Contains(cmd, "docker") && !strings.Contains(cmd, "test -f") {
					t.Errorf("good-docker-phase changed: %q", cmd)
				}
			} else {
				t.Error("good-docker-phase missing")
			}
		})
	}
}

func pingFreerideProxy(ctx context.Context, t *testing.T) bool {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:11434/api/tags", nil)
	if err != nil {
		return false
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}
