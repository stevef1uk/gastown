package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// resetModelsConfig clears the package-level cache so each subtest
// loads fresh from disk/env. Must be called with t.Cleanup.
func resetModelsConfig(t *testing.T) {
	t.Helper()
	origCfg := modelsConfig
	origPath := modelsConfigPath
	modelsConfig = nil
	modelsConfigPath = ""
	t.Cleanup(func() {
		modelsConfig = origCfg
		modelsConfigPath = origPath
	})
}

func TestGetModelsConfig_LoadsFromEnvVar(t *testing.T) {
	resetModelsConfig(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "models.json")
	want := ModelsConfig{
		Models: map[string]string{
			"planner": "test/planner-model",
			"judge":   "test/judge-model",
			"default": "test/default-model",
		},
		AgentModels: map[string]string{
			"gt-agent-local": "test/agent-model",
		},
	}
	data, _ := json.Marshal(want)
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GASTOWN_MODELS_CONFIG", cfgPath)

	got := GetModelsConfig()
	if got == nil {
		t.Fatal("expected config, got nil")
	}
	if got.Models["planner"] != "test/planner-model" {
		t.Fatalf("planner = %q, want %q", got.Models["planner"], "test/planner-model")
	}
	if got.AgentModels["gt-agent-local"] != "test/agent-model" {
		t.Fatalf("agent model = %q, want %q", got.AgentModels["gt-agent-local"], "test/agent-model")
	}
	if modelsConfigPath != cfgPath {
		t.Fatalf("modelsConfigPath = %q, want %q", modelsConfigPath, cfgPath)
	}
	// GetModel / GetAgentModel helpers must use the loaded config
	if m := GetModel("planner"); m != "test/planner-model" {
		t.Fatalf("GetModel(planner) = %q, want %q", m, "test/planner-model")
	}
	if m := GetModel("unknown-role"); m != "test/default-model" {
		t.Fatalf("GetModel(unknown) fallback = %q, want %q", m, "test/default-model")
	}
	if m := GetAgentModel("gt-agent-local"); m != "test/agent-model" {
		t.Fatalf("GetAgentModel = %q, want %q", m, "test/agent-model")
	}
}

func TestGetModelsConfig_EnvVarTakesPrecedenceOverDisk(t *testing.T) {
	resetModelsConfig(t)
	dir := t.TempDir()
	// Create a disk file that would be found via default candidates
	diskPath := filepath.Join(dir, "models.json")
	diskCfg := ModelsConfig{Models: map[string]string{"planner": "disk/planner", "default": "disk/default"}}
	data, _ := json.Marshal(diskCfg)
	_ = os.WriteFile(diskPath, data, 0644)

	// Create env-var file with different value
	envPath := filepath.Join(dir, "env-models.json")
	envCfg := ModelsConfig{Models: map[string]string{"planner": "env/planner", "default": "env/default"}}
	data, _ = json.Marshal(envCfg)
	if err := os.WriteFile(envPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GASTOWN_MODELS_CONFIG", envPath)

	// chdir so default candidate "models.json" resolves to diskPath
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	got := GetModelsConfig()
	if got.Models["planner"] != "env/planner" {
		t.Fatalf("env var should win: planner = %q, want env/planner", got.Models["planner"])
	}
}

func TestGetModelsConfig_NoFileReturnsEmptyNotHardcoded(t *testing.T) {
	resetModelsConfig(t)
	// Point env to non-existent file and ensure cwd has no models.json
	dir := t.TempDir()
	t.Setenv("GASTOWN_MODELS_CONFIG", filepath.Join(dir, "nonexistent.json"))
	origWd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })
	// Also clear HOME so ~/gt/gastown/models.json is not found
	t.Setenv("HOME", dir)

	got := GetModelsConfig()
	if got == nil {
		t.Fatal("expected empty config, got nil")
	}
	if len(got.Models) != 0 || len(got.AgentModels) != 0 {
		t.Fatalf("expected empty config when no file found, got Models=%v AgentModels=%v", got.Models, got.AgentModels)
	}
	// Must NOT contain hard-coded fallbacks
	if _, ok := got.Models["planner"]; ok {
		t.Fatal("hard-coded planner should not be present when no config file exists")
	}
	if m := GetModel("planner"); m != "" {
		t.Fatalf("GetModel with empty config should return empty, got %q", m)
	}
}

func TestGetModelsConfig_SingleSourceSymlink(t *testing.T) {
	// Verify the repo's two tracked paths resolve to the same content.
	// gastown/models.json is the canonical file; internal/config/models.json
	// must be a symlink (or identical copy) — not a divergent second source.
	repoRoot := filepath.Join("..", "..") // from internal/orchestrator -> gastown
	canonical := filepath.Join(repoRoot, "models.json")
	symlinkPath := filepath.Join(repoRoot, "internal", "config", "models.json")

	canonicalData, err := os.ReadFile(canonical)
	if err != nil {
		t.Skipf("canonical %s not found: %v", canonical, err)
	}
	symlinkData, err := os.ReadFile(symlinkPath)
	if err != nil {
		t.Fatalf("symlink path %s not readable: %v", symlinkPath, err)
	}
	if string(canonicalData) != string(symlinkData) {
		t.Fatalf("gastown/models.json and internal/config/models.json diverge — they must be a single source (symlink).\ncanonical (%s): %s\nsymlink target (%s): %s",
			canonical, string(canonicalData), symlinkPath, string(symlinkData))
	}
	// Also verify symlink is actually a symlink on disk (not a copy that could drift)
	if fi, err := os.Lstat(symlinkPath); err == nil {
		if fi.Mode()&os.ModeSymlink == 0 {
			t.Logf("warning: %s is not a symlink (mode %v) — consider using a symlink to prevent drift", symlinkPath, fi.Mode())
		}
	}
	var cfg ModelsConfig
	if err := json.Unmarshal(canonicalData, &cfg); err != nil {
		t.Fatalf("canonical JSON invalid: %v", err)
	}
	if _, ok := cfg.Models["planner"]; !ok {
		t.Fatal("canonical config must define planner model")
	}
	if _, ok := cfg.Models["judge"]; !ok {
		t.Fatal("canonical config must define judge model")
	}
}

func TestGetModel_FallbackToDefault(t *testing.T) {
	resetModelsConfig(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "models.json")
	cfg := ModelsConfig{
		Models: map[string]string{
			"default": "test/default-fallback",
			"judge":   "test/judge",
		},
	}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, data, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GASTOWN_MODELS_CONFIG", cfgPath)

	if got := GetModel("judge"); got != "test/judge" {
		t.Fatalf("GetModel(judge) = %q, want test/judge", got)
	}
	if got := GetModel("nonexistent-role"); got != "test/default-fallback" {
		t.Fatalf("GetModel fallback = %q, want test/default-fallback", got)
	}
	// Empty config -> empty default
	resetModelsConfig(t)
	emptyPath := filepath.Join(dir, "empty.json")
	_ = os.WriteFile(emptyPath, []byte(`{"models":{}}`), 0644)
	t.Setenv("GASTOWN_MODELS_CONFIG", emptyPath)
	if got := GetModel("planner"); got != "" {
		t.Fatalf("empty config should return empty model, got %q", got)
	}
}
