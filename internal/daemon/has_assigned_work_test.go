package daemon

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestHasAssignedOpenWork_UsesRepoRoutingInsteadOfRigFlag(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses Unix shell script mocks")
	}

	townRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(townRoot, ".beads"), 0o755); err != nil {
		t.Fatalf("mkdir town beads: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(townRoot, ".beads", "routes.jsonl"),
		[]byte("{\"prefix\":\"gt-\",\"path\":\"gastown/mayor/rig\"}\n"),
		0o644,
	); err != nil {
		t.Fatalf("write routes.jsonl: %v", err)
	}

	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "bd.log")
	expectedRepo := filepath.Join(townRoot, "gastown", "mayor", "rig")
	script := `#!/bin/sh
for arg in "$@"; do
  if [ "$arg" = "--repo=` + expectedRepo + `" ]; then
    repo_seen=1
  fi
  case "$arg" in
    --rig=*)
      echo "Error: unknown flag: --rig" >&2
      exit 1
      ;;
  esac
done
if [ "${repo_seen:-0}" -ne 1 ]; then
  echo "missing expected --repo flag" >&2
  exit 1
fi
printf '%s\n' "$*" >> "` + logPath + `"
printf '[{"id":"gt-123"}]\n'
`
	bdPath := filepath.Join(binDir, "bd")
	if err := os.WriteFile(bdPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}

	d := &Daemon{
		config: &Config{TownRoot: townRoot},
		bdPath: bdPath,
	}

	if !d.hasAssignedOpenWork("gastown", "polecats/rust") {
		t.Fatal("expected assigned work lookup to succeed")
	}

	logOutput, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read bd log: %v", err)
	}
	loggedArgs := string(logOutput)
	if strings.Contains(loggedArgs, "--rig=") {
		t.Fatalf("expected bd call to avoid --rig, got %q", loggedArgs)
	}
	if !strings.Contains(loggedArgs, "--repo="+expectedRepo) {
		t.Fatalf("expected bd call to include resolved --repo flag, got %q", loggedArgs)
	}
}
