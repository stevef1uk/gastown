package doctor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRigConfigSyncCheck_MissingConfig(t *testing.T) {
	// Create temp town root
	tmpDir := t.TempDir()
	mayorDir := filepath.Join(tmpDir, "mayor")
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create rigs.json with one rig
	rigsJSON := `{
		"version": 1,
		"rigs": {
			"testrig": {
				"git_url": "https://github.com/test/test.git",
				"added_at": "2026-03-01T00:00:00Z",
				"beads": {
					"prefix": "tr"
				}
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(mayorDir, "rigs.json"), []byte(rigsJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create town root config.json to avoid "hq missing config" warning
	townConfig := `{"type": "rig", "version": 1, "name": "hq", "beads": {"prefix": "hq"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(townConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// Create rig directory WITHOUT config.json
	rigDir := filepath.Join(tmpDir, "testrig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Run check
	ctx := &CheckContext{TownRoot: tmpDir}
	check := NewRigConfigSyncCheck()
	result := check.Run(ctx)

	if result.Status != StatusWarning {
		t.Errorf("expected StatusWarning, got %v", result.Status)
	}
	if len(check.missingConfig) != 1 {
		t.Errorf("expected 1 missing config, got %d", len(check.missingConfig))
	}
}

func TestRigConfigSyncCheck_FixCreatesConfig(t *testing.T) {
	// Create temp town root
	tmpDir := t.TempDir()
	mayorDir := filepath.Join(tmpDir, "mayor")
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create rigs.json with one rig
	rigsJSON := `{
		"version": 1,
		"rigs": {
			"testrig": {
				"git_url": "https://github.com/test/test.git",
				"added_at": "2026-03-01T00:00:00Z",
				"beads": {
					"prefix": "tr"
				}
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(mayorDir, "rigs.json"), []byte(rigsJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create town root config.json to avoid "hq missing config" warning
	townConfig := `{"type": "rig", "version": 1, "name": "hq", "beads": {"prefix": "hq"}}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(townConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// Create rig directory WITHOUT config.json
	rigDir := filepath.Join(tmpDir, "testrig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Run check and fix
	ctx := &CheckContext{TownRoot: tmpDir}
	check := NewRigConfigSyncCheck()
	result := check.Run(ctx)

	if result.Status != StatusWarning {
		t.Errorf("expected StatusWarning, got %v", result.Status)
	}

	// Fix
	if err := check.Fix(ctx); err != nil {
		t.Fatalf("fix failed: %v", err)
	}

	// Verify config.json was created
	configPath := filepath.Join(rigDir, "config.json")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("config.json was not created")
	}

	// Re-run check - should pass now
	result = check.Run(ctx)
	if result.Status != StatusOK {
		t.Errorf("expected StatusOK after fix, got %v: %s", result.Status, result.Message)
	}
}

func TestRigConfigSyncCheck_AllConfigsPresent(t *testing.T) {
	// Create temp town root
	tmpDir := t.TempDir()
	mayorDir := filepath.Join(tmpDir, "mayor")
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Install fake bd
	installFakeBdForDoctorTests(t, tmpDir)

	// Create rigs.json with one rig
	rigsJSON := `{
		"version": 1,
		"rigs": {
			"testrig": {
				"git_url": "https://github.com/test/test.git",
				"added_at": "2026-03-01T00:00:00Z",
				"beads": {
					"prefix": "tr"
				}
			}
		}
	}`
	if err := os.WriteFile(filepath.Join(mayorDir, "rigs.json"), []byte(rigsJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create town root config.json
	townCfg := `{
		"type": "town-settings",
		"version": 1,
		"beads": { "prefix": "hq" }
	}`
	if err := os.WriteFile(filepath.Join(tmpDir, "config.json"), []byte(townCfg), 0644); err != nil {
		t.Fatal(err)
	}

	// Create rig directory and config.json
	rigDir := filepath.Join(tmpDir, "testrig")
	if err := os.MkdirAll(filepath.Join(rigDir, "mayor", "rig", ".beads"), 0755); err != nil {
		t.Fatal(err)
	}
	configJSON := `{
		"type": "rig",
		"version": 1,
		"name": "testrig",
		"beads": { "prefix": "tr" }
	}`
	if err := os.WriteFile(filepath.Join(rigDir, "config.json"), []byte(configJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Mock identity beads for both hq and testrig
	writeFakeBeadOutput(t, filepath.Join(tmpDir, ".beads"), "hq-rig-hq")
	writeFakeBeadOutput(t, filepath.Join(rigDir, "mayor", "rig", ".beads"), "tr-rig-testrig")

	// Create metadata.json and config.yaml to avoid warnings
	metadata := `{"dolt_database": "testrig", "dolt_mode": "server"}`
	if err := os.WriteFile(filepath.Join(rigDir, "mayor", "rig", ".beads", "metadata.json"), []byte(metadata), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rigDir, "mayor", "rig", ".beads", "config.yaml"), []byte("issue-prefix: tr\n"), 0644); err != nil {
		t.Fatal(err)
	}
	hqMetadata := `{"dolt_database": "hq", "dolt_mode": "server"}`
	if err := os.MkdirAll(filepath.Join(tmpDir, ".beads"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".beads", "metadata.json"), []byte(hqMetadata), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, ".beads", "config.yaml"), []byte("issue-prefix: hq\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Run check
	ctx := &CheckContext{TownRoot: tmpDir}
	check := NewRigConfigSyncCheck()
	result := check.Run(ctx)

	if result.Status != StatusOK {
		t.Errorf("expected StatusOK, got %v: %s\nDetails: %v", result.Status, result.Message, result.Details)
	}
}

func TestStaleRuntimeFilesCheck_StalePIDFiles(t *testing.T) {
	// Create temp town root
	tmpDir := t.TempDir()
	mayorDir := filepath.Join(tmpDir, "mayor")
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create rigs.json with no rigs
	rigsJSON := `{"version": 1, "rigs": {}}`
	if err := os.WriteFile(filepath.Join(mayorDir, "rigs.json"), []byte(rigsJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create stale PID file for removed rig "pir"
	pidsDir := filepath.Join(tmpDir, ".runtime", "pids")
	if err := os.MkdirAll(pidsDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidsDir, "pir-witness.pid"), []byte("12345"), 0644); err != nil {
		t.Fatal(err)
	}

	// Create valid PID file for town agent
	if err := os.WriteFile(filepath.Join(pidsDir, "hq-deacon.pid"), []byte("12346"), 0644); err != nil {
		t.Fatal(err)
	}

	// Run check
	ctx := &CheckContext{TownRoot: tmpDir}
	check := NewStaleRuntimeFilesCheck()
	result := check.Run(ctx)

	if result.Status != StatusWarning {
		t.Errorf("expected StatusWarning, got %v", result.Status)
	}
	if len(check.stalePIDFiles) != 1 {
		t.Errorf("expected 1 stale PID file, got %d", len(check.stalePIDFiles))
	}
}

func TestStaleRuntimeFilesCheck_StaleWispConfig(t *testing.T) {
	// Create temp town root
	tmpDir := t.TempDir()
	mayorDir := filepath.Join(tmpDir, "mayor")
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create rigs.json with no rigs
	rigsJSON := `{"version": 1, "rigs": {}}`
	if err := os.WriteFile(filepath.Join(mayorDir, "rigs.json"), []byte(rigsJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create stale wisp config for removed rig "pir"
	wispDir := filepath.Join(tmpDir, ".beads-wisp", "config")
	if err := os.MkdirAll(wispDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wispDir, "pir.json"), []byte(`{"rig": "pir"}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Run check
	ctx := &CheckContext{TownRoot: tmpDir}
	check := NewStaleRuntimeFilesCheck()
	result := check.Run(ctx)

	if result.Status != StatusWarning {
		t.Errorf("expected StatusWarning, got %v", result.Status)
	}
	if len(check.staleWispConfigs) != 1 {
		t.Errorf("expected 1 stale wisp config, got %d", len(check.staleWispConfigs))
	}
}

func TestStaleRuntimeFilesCheck_Fix(t *testing.T) {
	// Create temp town root
	tmpDir := t.TempDir()
	mayorDir := filepath.Join(tmpDir, "mayor")
	if err := os.MkdirAll(mayorDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create rigs.json with no rigs
	rigsJSON := `{"version": 1, "rigs": {}}`
	if err := os.WriteFile(filepath.Join(mayorDir, "rigs.json"), []byte(rigsJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Create stale files
	pidsDir := filepath.Join(tmpDir, ".runtime", "pids")
	if err := os.MkdirAll(pidsDir, 0755); err != nil {
		t.Fatal(err)
	}
	stalePID := filepath.Join(pidsDir, "pir-witness.pid")
	if err := os.WriteFile(stalePID, []byte("12345"), 0644); err != nil {
		t.Fatal(err)
	}

	wispDir := filepath.Join(tmpDir, ".beads-wisp", "config")
	if err := os.MkdirAll(wispDir, 0755); err != nil {
		t.Fatal(err)
	}
	staleWisp := filepath.Join(wispDir, "pir.json")
	if err := os.WriteFile(staleWisp, []byte(`{"rig": "pir"}`), 0644); err != nil {
		t.Fatal(err)
	}

	// Run check and fix
	ctx := &CheckContext{TownRoot: tmpDir}
	check := NewStaleRuntimeFilesCheck()
	result := check.Run(ctx)

	if result.Status != StatusWarning {
		t.Errorf("expected StatusWarning, got %v", result.Status)
	}

	// Fix
	if err := check.Fix(ctx); err != nil {
		t.Fatalf("fix failed: %v", err)
	}

	// Verify files were removed
	if _, err := os.Stat(stalePID); !os.IsNotExist(err) {
		t.Error("stale PID file was not removed")
	}
	if _, err := os.Stat(staleWisp); !os.IsNotExist(err) {
		t.Error("stale wisp config was not removed")
	}

	// Re-run check - should pass
	result = check.Run(ctx)
	if result.Status != StatusOK {
		t.Errorf("expected StatusOK after fix, got %v", result.Status)
	}
}