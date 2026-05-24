package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

// polecatSchemaBeadTurn is the failure shape from testgt3: schema.go WRITE, markdown fence,
// schema_test.go WRITE, then shell verify/close — without ---END WRITE--- on the first file.
const polecatSchemaBeadTurn = `WRITE: linkshelf/internal/store/schema.go
package store

import (
	"database/sql"
	"fmt"
)

const schemaDDL = ` + "`" + `
CREATE TABLE IF NOT EXISTS links (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	title TEXT NOT NULL,
	url TEXT NOT NULL
);
` + "`" + `

func InitSchema(db *sql.DB) error {
	if _, err := db.Exec(schemaDDL); err != nil {
		return fmt.Errorf("init schema: %w", err)
	}
	return nil
}
` + "```\n" + `WRITE: linkshelf/internal/store/schema_test.go
` + "```" + `go
package store

import (
	"database/sql"
	"testing"
)

func TestInitSchema_compiles(t *testing.T) {
	var _ func(*sql.DB) error = InitSchema
}
` + "```\n" + `CMD: cd linkshelf && go mod tidy && go test -count=1 ./internal/store/...
CMD: export BEADS_DIR=$GT_ROOT/testgt3/.beads && cd testgt3/mayor/rig && bd close te-phq
`

func TestParseOrchestratedNativeEdits_writeRegression_table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		in        string
		wantOps   int
		wantPaths []string
		noCmdIn   []int // op indexes whose content must not contain CMD:
	}{
		{
			name: "polecat_schema_turn",
			in:   polecatSchemaBeadTurn,
			wantOps: 2,
			wantPaths: []string{
				"linkshelf/internal/store/schema.go",
				"linkshelf/internal/store/schema_test.go",
			},
			noCmdIn: []int{0, 1},
		},
		{
			name: "fence_then_write_then_cmd",
			in: "WRITE: linkshelf/a.go\npackage a\n```\n" +
				"WRITE: linkshelf/b.go\npackage b\n---END WRITE---\n" +
				"CMD: echo ok\n",
			wantOps:   2,
			wantPaths: []string{"linkshelf/a.go", "linkshelf/b.go"},
			noCmdIn:   []int{0, 1},
		},
		{
			name: "end_write_marker",
			in: "WRITE: linkshelf/a.go\npackage a\n---END WRITE---\n" +
				"CMD: true\n",
			wantOps:   1,
			wantPaths: []string{"linkshelf/a.go"},
			noCmdIn:   []int{0},
		},
		{
			name: "glued_end_write_cmd",
			in:   "WRITE: linkshelf/a.go\npackage a\n---END WRITE---CMD: echo glued\n",
			wantOps: 1, wantPaths: []string{"linkshelf/a.go"}, noCmdIn: []int{0},
		},
		{
			name: "read_between_writes",
			in: "WRITE: linkshelf/a.go\npackage a\n```\n" +
				"READ: linkshelf/b.go\n" +
				"WRITE: linkshelf/b.go\npackage b\n",
			wantOps:   3,
			wantPaths: []string{"linkshelf/a.go", "linkshelf/b.go", "linkshelf/b.go"},
			noCmdIn:   []int{0, 2},
		},
		{
			name: "split_write_terminator",
			in: "WRITE: tasklist/__init__.py\n# package\n---\nEND WRITE---\n" +
				"We need to add tests next.\n" +
				"WRITE: tasklist/tests/test_init.py\n# test\n---END WRITE---\n",
			wantOps:   2,
			wantPaths: []string{"tasklist/__init__.py", "tasklist/tests/test_init.py"},
			noCmdIn:   []int{0, 1},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := preprocessOrchestratedResponse(tc.in)
			ops := parseOrchestratedNativeEdits(in)
			if len(ops) != tc.wantOps {
				t.Fatalf("ops=%d want %d: %+v", len(ops), tc.wantOps, ops)
			}
			for i, wantPath := range tc.wantPaths {
				wantKind := "write"
				if tc.name == "read_between_writes" && i == 1 {
					wantKind = "read"
				}
				if ops[i].kind != wantKind {
					t.Fatalf("op[%d] kind=%q want %s", i, ops[i].kind, wantKind)
				}
				if !strings.Contains(ops[i].path, filepath.Base(wantPath)) {
					t.Fatalf("op[%d] path=%q want %q", i, ops[i].path, wantPath)
				}
			}
			for _, idx := range tc.noCmdIn {
				if strings.Contains(ops[idx].content, "CMD:") {
					t.Fatalf("op[%d] swallowed CMD: %q", idx, ops[idx].content)
				}
				if strings.Contains(ops[idx].content, "WRITE:") {
					t.Fatalf("op[%d] swallowed WRITE: %q", idx, ops[idx].content)
				}
				if strings.Contains(ops[idx].content, "We need to add") {
					t.Fatalf("op[%d] swallowed prose after split terminator: %q", idx, ops[idx].content)
				}
			}
			cmds := parseOrchestratedCommands(in)
			if strings.Contains(tc.in, "CMD:") && len(cmds) == 0 {
				t.Fatalf("expected CMD blocks, got none from %q", tc.in)
			}
			for _, cmd := range cmds {
				if strings.Contains(cmd, "WRITE:") || strings.Contains(cmd, "package store\n```") {
					t.Fatalf("CMD polluted by native tool body: %q", cmd)
				}
			}
		})
	}
}

func schemaBeadRunner(t *testing.T, dir, rig string) *stateRunner {
	t.Helper()
	mayor := filepath.Join(dir, rig, "mayor", "rig")
	if err := os.MkdirAll(filepath.Join(mayor, "linkshelf/internal/store"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mayor, "linkshelf/go.mod"), []byte("module linkshelf\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := linkshelfImplementValidation("linkshelf/internal/store/schema.go", "linkshelf/go.mod", "linkshelf/internal/store/store.go")
	v.QAVerifyCommand = "cd linkshelf && go test ./internal/store/..."
	task := rigFlowTask(t, "implementation", v)
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-phq"
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "open" || status == "in_progress" {
			return []orchestrator.PlanBead{{
				ID:    "te-phq",
				Title: "Implement linkshelf/internal/store/schema.go per architecture",
			}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })
	return r
}

func TestStateRunner_executeNativeEdits_schemaBeadDualWriteRegression(t *testing.T) {
	dir := t.TempDir()
	rig := "testgt3"
	r := schemaBeadRunner(t, dir, rig)
	workDir := rigMayorRigDir(dir, rig)

	ops := parseOrchestratedNativeEdits(preprocessOrchestratedResponse(polecatSchemaBeadTurn))
	if len(ops) != 2 {
		t.Fatalf("parse: ops=%+v", ops)
	}
	var combined strings.Builder
	r.executeNativeEdits(ops, workDir, "", nil, &combined)
	fb := combined.String()
	if !strings.Contains(fb, "WRITE: linkshelf/internal/store/schema.go") ||
		!strings.Contains(fb, "WRITE: linkshelf/internal/store/schema_test.go") {
		t.Fatalf("expected both WRITE ok lines, got:\n%s", fb)
	}
	if strings.Contains(fb, "rejected:") {
		t.Fatalf("native write rejected:\n%s", fb)
	}

	schemaPath := filepath.Join(workDir, "linkshelf/internal/store/schema.go")
	testPath := filepath.Join(workDir, "linkshelf/internal/store/schema_test.go")
	schemaData, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(schemaData), "WRITE:") || strings.Contains(string(schemaData), "CMD:") {
		t.Fatalf("schema.go polluted:\n%s", schemaData)
	}
	if !strings.Contains(string(schemaData), "func InitSchema") {
		t.Fatalf("schema.go missing InitSchema: %q", schemaData)
	}
	testData, err := os.ReadFile(testPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(testData), "```") {
		t.Fatalf("schema_test.go has fences: %q", testData)
	}
	if !strings.Contains(string(testData), "TestInitSchema") {
		t.Fatalf("schema_test.go missing test: %q", testData)
	}
}

func TestStateRunner_processOrchestratedTools_schemaTurnParsesVerifyAndClose(t *testing.T) {
	dir := t.TempDir()
	rig := "testgt3"
	r := schemaBeadRunner(t, dir, rig)

	in := preprocessOrchestratedResponse(polecatSchemaBeadTurn)
	cmds := parseOrchestratedCommands(in)
	if len(cmds) < 2 {
		t.Fatalf("want verify + bd close cmds, got %v", cmds)
	}
	var hasTest, hasClose bool
	for _, c := range cmds {
		if strings.Contains(c, "go test") {
			hasTest = true
		}
		if strings.Contains(c, "bd close") && strings.Contains(c, "te-phq") {
			hasClose = true
		}
	}
	if !hasTest || !hasClose {
		t.Fatalf("cmds=%v", cmds)
	}

	var combined strings.Builder
	hadNative, _, cmdCount := r.processOrchestratedTools(polecatSchemaBeadTurn, "sess", &combined)
	if !hadNative {
		t.Fatal("expected native edits")
	}
	if cmdCount < 2 {
		t.Fatalf("cmdCount=%d want >=2", cmdCount)
	}
	schemaPath := filepath.Join(rigMayorRigDir(dir, rig), "linkshelf/internal/store/schema_test.go")
	if _, err := os.Stat(schemaPath); err != nil {
		t.Fatalf("schema_test.go not created: %v", err)
	}
	fb := combined.String()
	if strings.Contains(fb, "rejected:") && strings.Contains(fb, "schema_test.go") {
		t.Fatalf("schema_test write rejected:\n%s", fb)
	}
	// bd close may fail without Dolt in CI; parsing + dual WRITE must still succeed.
	if strings.Contains(fb, "WRITE: linkshelf/internal/store/schema.go") &&
		!strings.Contains(fb, "WRITE: linkshelf/internal/store/schema_test.go") {
		t.Fatalf("second WRITE missing from feedback:\n%s", fb)
	}
}

func TestValidateImplementWritePath_rejectsSchemaTestWhileOnStoreBead(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	v := linkshelfImplementValidation("linkshelf/internal/store/store.go")
	v.QAVerifyCommand = "cd linkshelf && go test ./..."
	orchestrator.ListImplementBeadsByStatusHook = func(_, _ string, _ orchestrator.WorkflowValidation, status string) ([]orchestrator.PlanBead, error) {
		if status == "open" || status == "in_progress" {
			return []orchestrator.PlanBead{{
				ID:    "te-store",
				Title: "Implement linkshelf/internal/store/store.go per architecture",
			}}, nil
		}
		return nil, nil
	}
	t.Cleanup(func() { orchestrator.ListImplementBeadsByStatusHook = nil })
	// store_test.go is correlated to store.go, not schema.go — must not write while on store if we meant schema_test?
	// While on store bead, store_test.go IS allowed.
	if err := orchestrator.ValidateImplementWritePath(dir, rig, "te-store", "linkshelf/internal/store/store_test.go", v, true, ""); err != nil {
		t.Fatalf("store bead should allow store_test.go: %v", err)
	}
	// schema_test.go is not correlated to store.go
	if err := orchestrator.ValidateImplementWritePath(dir, rig, "te-store", "linkshelf/internal/store/schema_test.go", v, true, ""); err == nil {
		t.Fatal("expected reject schema_test.go while active bead is store.go")
	}
}
