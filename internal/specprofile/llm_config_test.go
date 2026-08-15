package specprofile

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/gastown/internal/config"
)

func TestResolveLLMForSpecIndex_envWins(t *testing.T) {
	t.Setenv("LLM_ENDPOINT", "http://example/v1/chat/completions")
	t.Setenv("LLM_MODEL", "test/model")
	// Isolate from user freeride_models.json
	t.Setenv("FREERIDE_MODELS_CONFIG", "/dev/null")
	e, m := ResolveLLMForSpecIndex(t.TempDir())
	if e != "http://example/v1/chat/completions" || m != "test/model" {
		t.Fatalf("got %q %q", e, m)
	}
}

func TestResolveLLMForSpecIndex_townArchitect(t *testing.T) {
	t.Setenv("LLM_ENDPOINT", "")
	t.Setenv("LLM_MODEL", "")
	// Isolate from user freeride_models.json
	t.Setenv("FREERIDE_MODELS_CONFIG", "/dev/null")
	townRoot := t.TempDir()
	ts := config.NewTownSettings()
	ts.Agents = config.DefaultFreerideAgents()
	ts.RoleAgents = map[string]string{"architect": "gt-agent-powerful"}
	if err := config.SaveTownSettings(config.TownSettingsPath(townRoot), ts); err != nil {
		t.Fatal(err)
	}
	e, m := ResolveLLMForSpecIndex(townRoot)
	if e != config.DefaultFreerideProxyEndpoint {
		t.Fatalf("endpoint: %q", e)
	}
	if m != "ollama/llama3.3" {
		t.Fatalf("model: %q", m)
	}
}

func TestHTTPTimeoutForSpecIndex_default(t *testing.T) {
	t.Setenv("GT_SPEC_INDEX_HTTP_TIMEOUT", "")
	if HTTPTimeoutForSpecIndex() != 5*time.Minute {
		t.Fatalf("want 5m, got %v", HTTPTimeoutForSpecIndex())
	}
}

func TestHTTPTimeoutForSpecIndex_override(t *testing.T) {
	t.Setenv("GT_SPEC_INDEX_HTTP_TIMEOUT", "90s")
	if HTTPTimeoutForSpecIndex().Seconds() != 90 {
		t.Fatalf("got %v", HTTPTimeoutForSpecIndex())
	}
}

func TestResolveLLMForSpecIndex_missingTownFreerideDefault(t *testing.T) {
	t.Setenv("LLM_ENDPOINT", "")
	t.Setenv("LLM_MODEL", "")
	// Isolate from user freeride_models.json
	t.Setenv("FREERIDE_MODELS_CONFIG", "/dev/null")
	dir := filepath.Join(t.TempDir(), "gt")
	e, m := ResolveLLMForSpecIndex(dir)
	if e != config.DefaultFreerideProxyEndpoint || m != "ollama/llama3.3" {
		t.Fatalf("got %q %q", e, m)
	}
	_ = os.MkdirAll(dir, 0755)
}
