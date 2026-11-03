package orchestrator

import (
	"os"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/llm"
	"github.com/steveyegge/gastown/internal/workspace"
)

func newJudgeClient(timeout time.Duration) *llm.Client {
	endpoint := strings.TrimSpace(os.Getenv("GT_JUDGE_ENDPOINT"))
	model := strings.TrimSpace(os.Getenv("GT_JUDGE_MODEL"))
	if endpoint == "" {
		endpoint = strings.TrimSpace(os.Getenv("LLM_ENDPOINT"))
	}
	if model == "" {
		model = strings.TrimSpace(os.Getenv("LLM_MODEL"))
	}
	if model == "" {
		model = GetModel("judge")
	}
	town := os.Getenv("GT_TOWN_ROOT")
	if town == "" {
		town = os.Getenv("GT_ROOT")
	}
	if town == "" {
		town, _ = workspace.FindFromCwdOrError()
	}
	for _, role := range []string{"qa", "architect"} {
		rc := config.ResolveRoleAgentConfig(role, town, "")
		if rc == nil {
			continue
		}
		if endpoint == "" {
			endpoint = strings.TrimSpace(rc.Env["LLM_ENDPOINT"])
		}
		if model == "" {
			model = strings.TrimSpace(rc.Env["LLM_MODEL"])
		}
	}
	if endpoint == "" {
		endpoint = config.DefaultFreerideProxyEndpoint
	}
	if model == "" {
		// Use the same configured Freeride defaults as spec-index when running
		// outside a town; never submit an empty model to the API.
		if rc := config.DefaultFreerideAgents(endpoint)["gt-agent-powerful"]; rc != nil {
			model = strings.TrimSpace(rc.Env["LLM_MODEL"])
		}
	}
	return llm.NewClient(endpoint, model, "judge", timeout)
}
