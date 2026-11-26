package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanRigSecrets_AllowSecretEscapeHatch(t *testing.T) {
	townRoot := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig", "app")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	shPath := filepath.Join(rigDir, "start.sh")
	shContent := `#!/usr/bin/env bash
if [[ -n "$USER_OPENROUTER_KEY" ]]; then
  export OPENROUTER_API_KEY="$USER_OPENROUTER_KEY" # allow-secret: env var pass-through, no literal secret value
fi
if [[ -n "$USER_MASSIVE_KEY" ]]; then
  export MASSIVE_API_KEY="$USER_MASSIVE_KEY" // allow-secret: env var pass-through
fi
`
	if err := os.WriteFile(shPath, []byte(shContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	findings := ScanRigSecrets(townRoot, rig, "app")
	if len(findings) > 0 {
		t.Errorf("expected 0 findings with allow-secret markers, got: %v", findings)
	}
}

func TestScanRigSecrets_CatchesSecretsWithoutAllow(t *testing.T) {
	townRoot := t.TempDir()
	rig := "testrig"
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig", "app")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	shPath := filepath.Join(rigDir, "leak.sh")
	shContent := `#!/usr/bin/env bash
export OPENROUTER_API_KEY="$USER_KEY"
`
	if err := os.WriteFile(shPath, []byte(shContent), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	findings := ScanRigSecrets(townRoot, rig, "app")
	if len(findings) == 0 {
		t.Errorf("expected findings for un-annotated secret assign, got none")
	}
}
