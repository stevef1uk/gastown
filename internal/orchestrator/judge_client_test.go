package orchestrator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/config"
)

func usePassingDocumentJudge(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"pass\":true,\"reason\":\"ok\"}"}}]}`))
	}))
	t.Cleanup(server.Close)
	t.Setenv("GT_JUDGE_ENDPOINT", server.URL)
	t.Setenv("GT_JUDGE_MODEL", "test/document-judge")
}

func TestJudgeUsesTownModelAndEndpoint(t *testing.T) {
	resetModelsConfig(t)
	modelsConfig = &ModelsConfig{}
	for _, key := range []string{"GT_JUDGE_ENDPOINT", "GT_JUDGE_MODEL", "LLM_ENDPOINT", "LLM_MODEL"} {
		t.Setenv(key, "")
	}
	t.Setenv("FREERIDE_MODELS_CONFIG", "/dev/null")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "configured/reviewer" {
			t.Errorf("judge sent model %q", request.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"{\"pass\":true,\"reason\":\"ok\"}"}}]}`))
	}))
	defer server.Close()
	town := t.TempDir()
	t.Setenv("GT_TOWN_ROOT", town)
	settings := config.NewTownSettings()
	settings.Agents = config.DefaultFreerideAgents(server.URL)
	settings.Agents["gt-agent-powerful"].Env["LLM_ENDPOINT"] = server.URL
	settings.Agents["gt-agent-powerful"].Env["LLM_MODEL"] = "configured/reviewer"
	settings.RoleAgents = map[string]string{"qa": "gt-agent-powerful"}
	if err := config.SaveTownSettings(config.TownSettingsPath(town), settings); err != nil {
		t.Fatal(err)
	}
	pass, _, err := ValidateDocumentWithJudge(context.Background(), newJudgeClient(time.Second), JudgeConfig{DocumentName: "architecture", Content: "test", Criteria: []string{"valid"}})
	if err != nil || !pass {
		t.Fatalf("judge: pass=%v err=%v", pass, err)
	}
}

func TestDockerJudgeConfigurationErrorIsNotApproval(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"model is required"}`, http.StatusBadRequest)
	}))
	defer server.Close()
	t.Setenv("GT_JUDGE_ENDPOINT", server.URL)
	t.Setenv("GT_JUDGE_MODEL", "configured/judge")
	arch := "# Architecture\n## Docker & Deployment\n" + strings.Repeat("Build the declared image and serve the application using its configured entrypoint and port. ", 4)
	problems := checkArchitectureDockerSection(arch, WorkflowValidation{RequiredFiles: []string{"app/Dockerfile"}})
	if len(problems) == 0 || !strings.Contains(problems[0], "could not validate") {
		t.Fatalf("configuration failure treated as approval: %v", problems)
	}
}
