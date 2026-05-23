package main

// End-to-end regression tests for READ:/EDIT:/WRITE: native tools.
// Covers preprocess → parse → scope → apply → disk, including polecat-shaped LLM output.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

type nativeEditE2ECase struct {
	name     string
	response string
	// setup writes files under mayor/rig (keys are repo-relative paths).
	setup map[string]string
	beads []orchestrator.PlanBead
	// activeBead empty → queue sync picks NextOpenImplementBead.
	activeBead string

	wantParseOps int // -1 = don't check
	wantParseErr bool

	wantFail              bool
	wantSuccessfulNative  bool
	wantFeedbackContains  []string
	wantFeedbackMissing   []string
	wantDiskContains      map[string][]string // path → substrings that must appear on disk
	wantDiskNotContains   map[string][]string
}

func linkshelfE2EValidation(files ...string) orchestrator.WorkflowValidation {
	return linkshelfImplementValidation(files...)
}

func writeMayorRigFiles(t *testing.T, mayor string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		abs := filepath.Join(mayor, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func readMayorRigFile(t *testing.T, mayor, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(mayor, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func newNativeEditE2ERunner(t *testing.T, dir, rig string, v orchestrator.WorkflowValidation, activeBead string, beads []orchestrator.PlanBead) *stateRunner {
	t.Helper()
	task := rigFlowTask(t, "implementation", v)
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = activeBead
	if len(beads) > 0 {
		stubImplementBeadsHook(t, beads...)
	}
	return r
}

func TestNativeEditE2E_parseTable(t *testing.T) {
	t.Parallel()
	cases := []nativeEditE2ECase{
		{
			name: "valid_edit_and_write",
			response: `EDIT: linkshelf/internal/store/store.go
<<<<<<< SEARCH
func Old() {}
=======
func New() {}
>>>>>>> REPLACE

WRITE: linkshelf/internal/new/x.go
package x
---END WRITE---
`,
			wantParseOps: 2,
		},
		{
			name: "prose_edit_path_not_parsed",
			response: "EDIT: command with `<<<<<<< SEARCH` / `=======` / `>>>>>>> REPLACE` blocks.\n",
			wantParseOps: 0,
		},
		{
			name:         "replace_only_not_parsed",
			response:     "EDIT: linkshelf/internal/api/handlers_test.go\n>>>>>>> REPLACE\n",
			wantParseOps: 0,
		},
		{
			name: "fenced_go_edit_parsed",
			response: "```go\nEDIT: linkshelf/internal/api/handlers_test.go\n<<<<<<< SEARCH\nx\n=======\ny\n>>>>>>> REPLACE\n```\n",
			wantParseOps: 1,
		},
		{
			name:         "prose_write_not_parsed",
			response:     "WRITE: ` command to create the file.\npackage x\n---END WRITE---\n",
			wantParseOps: 0,
		},
		{
			name: "end_edit_alias_parsed",
			response: `EDIT: linkshelf/internal/store/store_test.go
<<<<<<< SEARCH
old
=======
new
---END EDIT---
`,
			wantParseOps: 1,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ops := parseOrchestratedNativeEdits(tc.response)
			if tc.wantParseOps >= 0 && len(ops) != tc.wantParseOps {
				t.Fatalf("ops = %+v, want %d", ops, tc.wantParseOps)
			}
			if tc.wantParseOps == 0 && len(ops) > 0 {
				t.Fatalf("unexpected ops: %+v", ops)
			}
			if hint := FormatMalformedNativeEditFeedback(tc.response); tc.wantParseOps == 0 {
				if strings.Contains(tc.name, "prose_edit") || strings.Contains(tc.name, "replace_only") {
					if hint == "" {
						t.Fatal("expected malformed EDIT feedback")
					}
				}
			}
		})
	}
}

func TestNativeEditE2E_computeSearchReplace(t *testing.T) {
	t.Parallel()
	t.Run("unique_match", func(t *testing.T) {
		t.Parallel()
		updated, msg, err := computeNativeSearchReplace("alpha\nbeta\ngamma\n", "beta", "BETA")
		if err != nil {
			t.Fatal(err)
		}
		if updated != "alpha\nBETA\ngamma\n" || !strings.Contains(msg, "1 search/replace") {
			t.Fatalf("updated=%q msg=%q", updated, msg)
		}
	})
	t.Run("not_found", func(t *testing.T) {
		t.Parallel()
		_, _, err := computeNativeSearchReplace("hello\n", "missing", "x")
		if err == nil || !strings.Contains(err.Error(), "SEARCH block not found") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("duplicate_match", func(t *testing.T) {
		t.Parallel()
		_, _, err := computeNativeSearchReplace("dup\ndup\n", "dup", "x")
		if err == nil || !strings.Contains(err.Error(), "matches 2 times") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("crlf_normalized", func(t *testing.T) {
		t.Parallel()
		// SEARCH uses LF only; file has CRLF — must take the normalized line-ending path.
		updated, msg, err := computeNativeSearchReplace("a\r\nb\r\n", "a\nb", "A\nB")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(updated, "A") || !strings.Contains(updated, "B") {
			t.Fatalf("updated=%q", updated)
		}
		if !strings.Contains(msg, "normalized") {
			t.Fatalf("msg=%q", msg)
		}
	})
}

func TestNativeEditE2E_processOrchestratedTools(t *testing.T) {
	relStore := "linkshelf/internal/store/store.go"
	relSchema := "linkshelf/internal/store/schema.go"
	relSchemaTest := "linkshelf/internal/store/schema_test.go"
	relHandlersTest := "linkshelf/internal/api/handlers_test.go"

	storeBody := "package store\n\nvar version = 1\n"
	schemaBody := "package store\n\nvar schemaVersion = 1\n"
	schemaTestBody := "package store_test\n\nfunc TestSchema(t *testing.T) {}\n"
	handlersTestBody := "package api_test\n\nfunc TestHandlers(t *testing.T) {}\n"

	beadStore := orchestrator.PlanBead{ID: "te-store", Title: "Implement linkshelf/internal/store/store.go per architecture"}
	beadSchema := orchestrator.PlanBead{ID: "te-8cz", Title: "Implement linkshelf/internal/store/schema.go per architecture"}
	beadHandlersTest := orchestrator.PlanBead{ID: "te-rnd", Title: "Implement linkshelf/internal/api/handlers_test.go per architecture"}

	cases := []nativeEditE2ECase{
		{
			name: "happy_edit_applies_to_disk",
			setup: map[string]string{
				relStore: storeBody,
				"linkshelf/go.mod": "module linkshelf\n\ngo 1.22\n",
			},
			beads:      []orchestrator.PlanBead{beadStore},
			activeBead: "te-store",
			response: `EDIT: linkshelf/internal/store/store.go
<<<<<<< SEARCH
var version = 1
=======
var version = 2
>>>>>>> REPLACE
`,
			wantSuccessfulNative: true,
			wantDiskContains:     map[string][]string{relStore: {"var version = 2"}},
			wantDiskNotContains:  map[string][]string{relStore: {"var version = 1"}},
		},
		{
			name: "in_progress_cmd_before_edit_in_message",
			setup: map[string]string{
				relSchema: schemaBody,
			},
			beads:      []orchestrator.PlanBead{beadSchema},
			activeBead: "",
			response: `EDIT: linkshelf/internal/store/schema.go
<<<<<<< SEARCH
var schemaVersion = 1
=======
var schemaVersion = 2
>>>>>>> REPLACE
CMD: export BEADS_DIR=x && cd mockrig/mayor/rig && bd update te-8cz --status=in_progress
`,
			wantSuccessfulNative: true,
			wantDiskContains:     map[string][]string{relSchema: {"schemaVersion = 2"}},
			wantFeedbackContains: []string{"EDIT:"},
		},
		{
			name:     "reject_prose_edit_path",
			setup:    map[string]string{relHandlersTest: handlersTestBody},
			beads:    []orchestrator.PlanBead{beadHandlersTest},
			activeBead: "te-rnd",
			response: "EDIT: command with `<<<<<<< SEARCH` / `=======` / `>>>>>>> REPLACE` blocks.\n",
			wantFail: true,
			wantFeedbackContains: []string{"rejected prose path", "Malformed EDIT"},
			wantDiskContains:     map[string][]string{relHandlersTest: {"func TestHandlers"}},
			wantDiskNotContains:  map[string][]string{relHandlersTest: {"SEARCH"}},
		},
		{
			name: "reject_wrong_bead_path",
			setup: map[string]string{
				relSchema:       schemaBody,
				relHandlersTest: handlersTestBody,
			},
			beads:      []orchestrator.PlanBead{beadSchema, beadHandlersTest},
			activeBead: "te-8cz",
			response: `EDIT: linkshelf/internal/api/handlers_test.go
<<<<<<< SEARCH
func TestHandlers(t *testing.T) {}
=======
func TestHandlers(t *testing.T) { t.Run("x", func(t *testing.T) {}) }
>>>>>>> REPLACE
`,
			wantFail: true,
			wantFeedbackContains: []string{
				"write only the active/next implement file",
				"te-rnd",
				"te-8cz",
				"handlers_test.go",
			},
			wantDiskNotContains: map[string][]string{
				relHandlersTest: {"t.Run(\"x\""},
			},
		},
		{
			name: "correlated_test_file_on_schema_bead",
			setup: map[string]string{
				relSchema:     schemaBody,
				relSchemaTest: schemaTestBody,
			},
			beads:      []orchestrator.PlanBead{beadSchema},
			activeBead: "te-8cz",
			response: `EDIT: linkshelf/internal/store/schema_test.go
<<<<<<< SEARCH
func TestSchema(t *testing.T) {}
=======
func TestSchema(t *testing.T) { t.Run("ok", func(t *testing.T) {}) }
>>>>>>> REPLACE
`,
			wantSuccessfulNative: true,
			wantDiskContains: map[string][]string{
				relSchemaTest: {`t.Run("ok"`},
			},
		},
		{
			name: "search_miss_auto_read",
			setup: map[string]string{
				relHandlersTest: handlersTestBody,
			},
			beads:      []orchestrator.PlanBead{beadHandlersTest},
			activeBead: "te-rnd",
			response: `EDIT: linkshelf/internal/api/handlers_test.go
<<<<<<< SEARCH
func TestMissing(t *testing.T) {}
=======
func TestY(t *testing.T) {}
>>>>>>> REPLACE
`,
			wantFail: true,
			wantFeedbackContains: []string{
				"SEARCH block not found",
				"Auto-READ",
				"func TestHandlers",
			},
		},
		{
			name: "read_then_edit",
			setup: map[string]string{
				relStore: storeBody,
				"linkshelf/go.mod": "module linkshelf\n\ngo 1.22\n",
			},
			beads:      []orchestrator.PlanBead{beadStore},
			activeBead: "te-store",
			response: `READ: linkshelf/internal/store/store.go

EDIT: linkshelf/internal/store/store.go
<<<<<<< SEARCH
var version = 1
=======
var version = 9
>>>>>>> REPLACE
`,
			wantSuccessfulNative: true,
			wantFeedbackContains: []string{"package store", "EDIT:"},
			wantDiskContains:     map[string][]string{relStore: {"var version = 9"}},
		},
		{
			name: "write_new_file",
			setup: map[string]string{
				"linkshelf/go.mod": "module linkshelf\n\ngo 1.22\n",
			},
			beads: []orchestrator.PlanBead{{
				ID:    "te-new",
				Title: "Implement linkshelf/internal/new/pkg.go per architecture",
			}},
			activeBead: "te-new",
			response: `WRITE: linkshelf/internal/new/pkg.go
package newpkg

func Init() {}
---END WRITE---
`,
			wantSuccessfulNative: true,
			wantDiskContains: map[string][]string{
				"linkshelf/internal/new/pkg.go": {"package newpkg", "func Init()"},
			},
		},
		{
			name: "fenced_edit_applies",
			setup: map[string]string{
				relStore: storeBody,
			},
			beads:      []orchestrator.PlanBead{beadStore},
			activeBead: "te-store",
			response: "```go\nEDIT: linkshelf/internal/store/store.go\n<<<<<<< SEARCH\nvar version = 1\n=======\nvar version = 3\n>>>>>>> REPLACE\n```\n",
			wantSuccessfulNative: true,
			wantDiskContains:     map[string][]string{relStore: {"var version = 3"}},
		},
		{
			name: "edit_strips_markdown_from_replace_body",
			setup: map[string]string{
				relStore: "package store\n\nfunc Old() {}\n",
			},
			beads:      []orchestrator.PlanBead{beadStore},
			activeBead: "te-store",
			response: "EDIT: linkshelf/internal/store/store.go\n<<<<<<< SEARCH\nfunc Old() {}\n=======\n" +
				"```go\nfunc New() {}\n```\nEOF\n>>>>>>> REPLACE\n",
			wantSuccessfulNative: true,
			wantDiskContains:     map[string][]string{relStore: {"func New()"}},
			wantDiskNotContains:  map[string][]string{relStore: {"```", "EOF", "func Old()"}},
		},
		{
			name: "malformed_replace_only_turn",
			response: "`bd update te-rnd --status=in_progress`\nEDIT: linkshelf/internal/api/handlers_test.go\n>>>>>>> REPLACE\n",
			setup:    map[string]string{relHandlersTest: handlersTestBody},
			beads:    []orchestrator.PlanBead{beadHandlersTest},
			activeBead: "te-rnd",
			wantFail: true,
			wantFeedbackContains: []string{
				"Malformed EDIT",
				"<<<<<<< SEARCH",
				"handlers_test.go",
			},
			wantDiskNotContains: map[string][]string{
				relHandlersTest: {">>>>>>> REPLACE"},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			rig := "mockrig"
			mayor := filepath.Join(dir, rig, "mayor", "rig")
			if tc.setup != nil {
				writeMayorRigFiles(t, mayor, tc.setup)
			}

			var required []string
			for p := range tc.setup {
				required = append(required, p)
			}
			for _, b := range tc.beads {
				if p := orchestrator.ExtractPathFromBeadTitle(b.Title, "Implement "); p != "" {
					found := false
					for _, r := range required {
						if r == p {
							found = true
							break
						}
					}
					if !found {
						required = append(required, p)
					}
				}
			}
			if len(required) == 0 {
				required = []string{relStore}
			}
			v := linkshelfE2EValidation(required...)
			r := newNativeEditE2ERunner(t, dir, rig, v, tc.activeBead, tc.beads)

			var combined strings.Builder
			hadNative, hadSuccess, _ := r.processOrchestratedTools(tc.response, "sess", &combined)
			feedback := combined.String()

			if tc.wantSuccessfulNative && (!hadNative || !hadSuccess) {
				t.Fatalf("want successful native edit; hadNative=%v hadSuccess=%v\n%s", hadNative, hadSuccess, feedback)
			}
			if !tc.wantSuccessfulNative && hadSuccess {
				t.Fatalf("unexpected successful native edit:\n%s", feedback)
			}
			if tc.wantFail && !r.track.hadCmdFailure && !strings.Contains(feedback, "Error:") && !strings.Contains(feedback, "Malformed EDIT") {
				t.Fatalf("want failure tracked or error feedback:\n%s", feedback)
			}

			for _, sub := range tc.wantFeedbackContains {
				if !strings.Contains(feedback, sub) {
					t.Fatalf("feedback missing %q:\n%s", sub, feedback)
				}
			}
			for _, sub := range tc.wantFeedbackMissing {
				if strings.Contains(feedback, sub) {
					t.Fatalf("feedback must not contain %q:\n%s", sub, feedback)
				}
			}
			for path, subs := range tc.wantDiskContains {
				got := readMayorRigFile(t, mayor, path)
				for _, sub := range subs {
					if !strings.Contains(got, sub) {
						t.Fatalf("%s missing %q in:\n%s", path, sub, got)
					}
				}
			}
			for path, subs := range tc.wantDiskNotContains {
				got := readMayorRigFile(t, mayor, path)
				for _, sub := range subs {
					if strings.Contains(got, sub) {
						t.Fatalf("%s must not contain %q in:\n%s", path, sub, got)
					}
				}
			}
		})
	}
}

// TestNativeEditE2E_applyOnDisk_isolated exercises applyNativeSearchReplace without scope/beads.
func TestNativeEditE2E_applyOnDisk_isolated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "linkshelf/internal/api/handlers_test.go")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	body := "package api_test\n\nfunc TestHandlers(t *testing.T) {\n\t// empty\n}\n"
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	search := "\t// empty"
	replace := "\treq, _ := http.NewRequest(\"POST\", \"/api/links\", nil)"
	msg, err := applyNativeSearchReplace(path, search, replace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "1 search/replace") {
		t.Fatalf("msg=%q", msg)
	}
	got := readMayorRigFile(t, dir, "linkshelf/internal/api/handlers_test.go")
	if !strings.Contains(got, "/api/links") {
		t.Fatalf("replace not applied:\n%s", got)
	}
	if strings.Contains(got, "// empty") {
		t.Fatalf("search still present:\n%s", got)
	}
}
