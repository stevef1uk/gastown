package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDesignEntryPreservesExistingWork(t *testing.T) {
	for _, load := range []func() (*WorkflowTemplate, error){LoadRigFlowTemplate, LoadReqFlowTemplate} {
		tpl, err := load()
		if err != nil {
			t.Fatal(err)
		}
		t.Run(tpl.ID, func(t *testing.T) {
			town := t.TempDir()
			rigDir := filepath.Join(town, "example", "mayor", "rig")
			files := map[string]string{
				"architecture.md":       "# Existing architecture\nApproved contracts stay intact.\n",
				"app/main.py":           "def approved_api(): pass\n",
				"app/main.go":           "package main\n",
				"app/src/dashboard.tsx": "export const Dashboard = () => null;\n",
				"app/test/e2e.spec.ts":  "test('approved flow', () => {});\n",
				"app/web/index.html":    "<h1>Approved UI</h1>\n",
				"app/data/portfolio.db": "existing portfolio data",
			}
			for path, content := range files {
				full := filepath.Join(rigDir, path)
				if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte(content), 0644); err != nil {
					t.Fatal(err)
				}
			}
			v := WorkflowValidation{LayoutRoot: "app"}
			for _, hook := range tpl.States["design"].Hooks.PreRun {
				if hook == "reset_layout_pre_implementation" {
					t.Fatal("repeatable design state must not reset implementation")
				}
				if _, err := RunPreRunHook(hook, town, "example", v); err != nil {
					t.Fatal(err)
				}
			}
			// Cached tasks/unsynced town templates must also be safe after upgrade.
			if _, err := RunPreRunHook("reset_layout_pre_implementation", town, "example", v); err != nil {
				t.Fatal(err)
			}
			for path, want := range files {
				got, err := os.ReadFile(filepath.Join(rigDir, path))
				if err != nil || string(got) != want {
					t.Errorf("design entry changed %s: content=%q err=%v", path, got, err)
				}
			}
		})
	}
}

func TestDesignDraftContextRequiresFullReadBeforeAmendment(t *testing.T) {
	town := t.TempDir()
	rigDir := filepath.Join(town, "example", "mayor", "rig")
	if err := os.MkdirAll(rigDir, 0755); err != nil {
		t.Fatal(err)
	}
	if got := FormatDesignDraftContextBlock(town, "example"); got != "" {
		t.Fatalf("new design must not receive existing-document instructions: %s", got)
	}
	doc := "# Architecture\n" + strings.Repeat("Approved contract.\n", 500) + "TAIL-CONTRACT\n"
	archPath := filepath.Join(rigDir, "architecture.md")
	if err := os.WriteFile(archPath, []byte(doc), 0644); err != nil {
		t.Fatal(err)
	}
	block := FormatDesignDraftContextBlock(town, "example")
	for _, want := range []string{"CMD: cat " + filepath.ToSlash(archPath), "truncated preview", "targeted", "completed-phase contracts", "full-file heredoc is NOT required", "... (truncated)"} {
		if !strings.Contains(block, want) {
			t.Errorf("existing-document context missing %q: %s", want, block)
		}
	}
	if strings.Contains(block, "TAIL-CONTRACT") || strings.Contains(block, "rewrite the complete file") {
		t.Fatal("truncated preview must not invite a complete document rewrite")
	}
	got, err := os.ReadFile(archPath)
	if err != nil || string(got) != doc {
		t.Fatal("building draft context changed the document")
	}
}
