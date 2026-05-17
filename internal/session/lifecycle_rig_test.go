package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRepairTownRoleRigIdentity_writesGtAgent(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	ctx := context.Background()

	if err := RepairTownRoleRigIdentity(ctx, nil, "", workDir, "polecat", "mockrig"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(workDir, ".gt-agent"))
	if err != nil {
		t.Fatal(err)
	}
	var id struct {
		Role string `json:"role"`
		Rig  string `json:"rig"`
	}
	if err := json.Unmarshal(data, &id); err != nil {
		t.Fatal(err)
	}
	if id.Role != "polecat" || id.Rig != "mockrig" {
		t.Fatalf("identity = %+v, want role=polecat rig=mockrig", id)
	}
}

func TestRepairTownRoleRigIdentity_skipsWhenRigMatches(t *testing.T) {
	t.Parallel()
	workDir := t.TempDir()
	existing := `{"role":"polecat","rig":"mockrig"}`
	if err := os.WriteFile(filepath.Join(workDir, ".gt-agent"), []byte(existing), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RepairTownRoleRigIdentity(context.Background(), nil, "", workDir, "polecat", "mockrig"); err != nil {
		t.Fatal(err)
	}
}
