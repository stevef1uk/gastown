package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func installFakeBdForDoctorTests(t *testing.T, townRoot string) {
	t.Helper()

	binDir := filepath.Join(townRoot, "bin")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir fake bin: %v", err)
	}

	script := `#!/bin/sh
set -eu

target="${BEADS_DIR:-$PWD}"
if [ -d "$target/.beads" ]; then
  target="$target/.beads"
fi

case "$1:$2:$3" in
  config:get:types.custom)
    if [ -f "$target/types.custom" ]; then
      cat "$target/types.custom"
    else
      exit 1
    fi
    ;;
  config:set:types.custom)
    printf '%s\n' "$4" > "$target/types.custom"
    ;;
  config:get:status.custom)
    if [ -f "$target/status.custom" ]; then
      cat "$target/status.custom"
    else
      exit 1
    fi
    ;;
  config:set:status.custom)
    printf '%s\n' "$4" > "$target/status.custom"
    ;;
  show:*:--json)
    # Extract ID from $2 (e.g., hq-rig-gastown)
    id="$2"
    if [ -f "$target/show-$id.json" ]; then
      cat "$target/show-$id.json"
    else
      echo "issue not found" >&2
      exit 1
    fi
    ;;
  *)
    # Fallback for other commands used in Fix
    exit 0
    ;;
esac
`

	bdPath := filepath.Join(binDir, "bd")
	if err := os.WriteFile(bdPath, []byte(script), 0755); err != nil {
		t.Fatalf("write fake bd: %v", err)
	}

	oldPath := os.Getenv("PATH")
	if err := os.Setenv("PATH", fmt.Sprintf("%s:%s", binDir, oldPath)); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("PATH", oldPath)
	})
	for _, key := range []string{"BEADS_DIR", "BEADS_DB", "BEADS_DOLT_SERVER_DATABASE"} {
		oldVal, hadVal := os.LookupEnv(key)
		_ = os.Unsetenv(key)
		t.Cleanup(func() {
			if hadVal {
				_ = os.Setenv(key, oldVal)
			} else {
				_ = os.Unsetenv(key)
			}
		})
	}
}

func writeFakeBeadOutput(t *testing.T, beadsDir, id string) {
	t.Helper()
	if err := os.MkdirAll(beadsDir, 0755); err != nil {
		t.Fatalf("mkdir beads dir: %v", err)
	}
	content := fmt.Sprintf(`{"id": "%s", "title": "test", "labels": ["gt:rig"]}`, id)
	if err := os.WriteFile(filepath.Join(beadsDir, "show-"+id+".json"), []byte(content), 0644); err != nil {
		t.Fatalf("write fake bead output: %v", err)
	}
}
