package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestParseOrchestratedCommandsPreservesLiteralFences(t *testing.T) {
	commands := []string{
		"sed -i '/^## Delivery phases$/,/^## Requirements$/ { /^yaml$/s/^yaml$/```yaml/; /^## Requirements$/i\\\n```\\\n}' architecture.md",
		"test \"$(grep -c '^```yaml$' architecture.md)\" -eq 1 && test \"$(grep -c '^```$' architecture.md)\" -eq 1",
		"printf '%s\\n' '```json' '```JSON' '```cmd literal' '```'",
	}
	for _, cmd := range commands {
		t.Run(cmd, func(t *testing.T) {
			parsed := parseOrchestratedCommands("CMD: " + cmd + "\n\n{\"outcome\":\"success\",\"summary\":\"done\"}")
			if len(parsed) != 1 || parsed[0] != cmd {
				t.Fatalf("shell payload changed: got %q, want %q", parsed, cmd)
			}
		})
	}
}

func TestArchitectShellRepairProducesValidDeliveryContract(t *testing.T) {
	town, rig := t.TempDir(), "sample"
	dir := filepath.Join(town, rig, "mayor", "rig")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	doc := "# Architecture\n## Delivery phases\nyaml\ndelivery_phases:\n  - id: core\n    required_files: [demo/main.go]\n    qa_verify_command: cd demo && go build ./...\n## Requirements\n### core\nBuild the application.\n"
	path := filepath.Join(dir, "architecture.md")
	if err := os.WriteFile(path, []byte(doc), 0644); err != nil {
		t.Fatal(err)
	}
	response := "CMD: cd " + dir + " && sed -i.bak '/^yaml$/s/^yaml$/```yaml/; /^## Requirements$/i\\\n```' architecture.md\n\n" +
		"CMD: cd " + dir + " && test \"$(grep -c '^```yaml$' architecture.md)\" -eq 1 && test \"$(grep -c '^```$' architecture.md)\" -eq 1\n"
	r := &stateRunner{
		townRoot: town, rig: rig,
		hooks: orchestrator.StateHooks{CmdGuard: "design", CmdRewrites: []string{"rig_placeholders", "arch_md_after_cd"}},
	}
	commands := parseOrchestratedCommands(response)
	if len(commands) != 2 {
		t.Fatalf("expected repair and verification commands: %q", commands)
	}
	for _, cmd := range commands {
		cmd = r.rewriteCommand(cmd)
		if err := r.validateCommand(cmd); err != nil {
			t.Fatal(err)
		}
		proc := exec.Command("sh", "-c", cmd)
		proc.Dir = town
		if out, err := proc.CombinedOutput(); err != nil {
			t.Fatalf("repair command failed: %v\n%s\n%s", err, cmd, out)
		}
	}
	fixed, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(fixed), "```yaml\ndelivery_phases:") {
		t.Fatalf("repair did not write a fenced contract:\n%s", fixed)
	}
	if err := orchestrator.ValidateArchitectureStructure(string(fixed)); err != nil {
		t.Fatalf("repaired contract failed design validation: %v", err)
	}
}
