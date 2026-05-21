package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

func TestIsStrayFileTerminatorLine(t *testing.T) {
	t.Parallel()
	yes := []string{"EOF", "eof", "EOT", "end", "---END WRITE---", "---END EDIT---", "  EOF  "}
	for _, line := range yes {
		if !isStrayFileTerminatorLine(line) {
			t.Errorf("want stray: %q", line)
		}
	}
	no := []string{"", "package EOF", "const x = \"EOF\"", "```", "```go", "EOF marker in comment"}
	for _, line := range no {
		if isStrayFileTerminatorLine(line) {
			t.Errorf("want not stray: %q", line)
		}
	}
}

func TestSanitizeNativeFileContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		in         string
		want       string
		unchanged  bool
		mustNotHas []string
	}{
		{name: "empty", in: "", want: "", unchanged: true},
		{name: "clean_go_unchanged", in: "package store\n\nimport \"fmt\"\n", want: "package store\n\nimport \"fmt\"\n", unchanged: true},
		{name: "trailing_eof_only", in: "package main\nEOF\n", want: "package main\n"},
		{name: "trailing_eot", in: "x = 1\nEOT\n", want: "x = 1\n"},
		{name: "trailing_end", in: "x = 1\nEND\n", want: "x = 1\n"},
		{name: "trailing_end_write", in: "package foo\n---END WRITE---\n", want: "package foo\n"},
		{name: "trailing_end_edit", in: "package foo\n---END EDIT---\n", want: "package foo\n"},
		{name: "fences_go_only", in: "```go\npackage store\n```\n", want: "package store\n"},
		{name: "fences_then_eof", in: "```go\npackage store\n```\nEOF\n", want: "package store\n", mustNotHas: []string{"```", "EOF"}},
		{name: "eof_then_fence", in: "package store\nEOF\n```\n", want: "package store\n", mustNotHas: []string{"```", "EOF"}},
		{name: "triple_junk_stack", in: "```go\npackage x\n```\nEOF\n---END WRITE---\n", want: "package x\n", mustNotHas: []string{"```", "EOF", "END WRITE"}},
		{name: "python_fence", in: "```python\ndef f():\n    pass\n```\n", want: "def f():\n    pass\n"},
		{name: "no_trailing_newline", in: "```go\npackage x\n```", want: "package x"},
		{name: "preserve_eof_in_string", in: "package main\n\nconst Delim = \"EOF\"\n", want: "package main\n\nconst Delim = \"EOF\"\n", unchanged: true},
		{
			name: "preserve_midfile_eof_comment",
			in:   "package main\n\n// Document stops at EOF in spec\nfunc main() {}\n",
			want: "package main\n\n// Document stops at EOF in spec\nfunc main() {}\n",
			unchanged: true,
		},
		{
			name: "preserve_eof_line_not_at_end",
			in:   "package main\nEOF\nfunc init() {}\n",
			want: "package main\nEOF\nfunc init() {}\n",
			unchanged: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := sanitizeNativeFileContent(tt.in)
			if tt.unchanged && got != tt.in {
				t.Fatalf("want unchanged %q got %q", tt.in, got)
			}
			if !tt.unchanged && got != tt.want {
				t.Fatalf("want %q got %q", tt.want, got)
			}
			for _, bad := range tt.mustNotHas {
				if strings.Contains(got, bad) {
					t.Fatalf("must not contain %q in %q", bad, got)
				}
			}
		})
	}
}

func TestSanitizeNativeFileContent_realWorldStoreSnippet(t *testing.T) {
	// Regression: polecat wrote user's store.go with ```go prefix and ```/EOF suffix.
	in := "```go\npackage store\n\nimport (\n\t\"database/sql\"\n\t\"fmt\"\n)\n\ntype Bookmark struct{}\n```\nEOF\n"
	got := sanitizeNativeFileContent(in)
	if !strings.HasPrefix(got, "package store") {
		preview := got
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		t.Fatalf("want package first: %q", preview)
	}
	if strings.Contains(got, "```") || strings.HasSuffix(strings.TrimSpace(got), "EOF") {
		t.Fatalf("junk remains: %q", got)
	}
	if !strings.Contains(got, "database/sql") {
		t.Fatal("expected real imports preserved")
	}
}

func TestStripMarkdownFencesInHeredocScripts_cases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		script     string
		wantBody   string
		mustNotHas []string
	}{
		{
			name:     "fences_in_body",
			script:   "cat > f.go <<'EOF'\n```go\npackage main\n```\nEOF\n",
			wantBody: "package main",
		},
		{
			name:     "stray_eof_before_delimiter",
			script:   "cat > f.go <<'EOF'\npackage main\n\nEOF\nEOF\n",
			wantBody: "package main",
		},
		{
			name:     "fences_eof_stack",
			script:   "cat > f.go <<'EOF'\n```go\npackage x\n```\nEOF\nEOF\n",
			wantBody: "package x",
			mustNotHas: []string{"```", "\nEOF\nEOF\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stripMarkdownFencesInHeredocScripts(tt.script)
			body := heredocBodyForTest(t, got, "<<'EOF'")
			if strings.TrimSpace(body) != tt.wantBody {
				t.Fatalf("body=%q want %q (full script %q)", body, tt.wantBody, got)
			}
			for _, bad := range tt.mustNotHas {
				if strings.Contains(body, bad) {
					t.Fatalf("body contains %q: %q", bad, body)
				}
			}
		})
	}
}

func TestPrepareOrchestratedScript_sanitizesHeredocGoBody(t *testing.T) {
	in := "cd rig/mayor/rig && cat > linkshelf/internal/store/store.go <<'EOF'\n```go\npackage store\nimport \"fmt\"\n```\nEOF\nEOF\n"
	got := prepareOrchestratedScript(in)
	body := heredocBodyForTest(t, got, "<<'EOF'")
	if strings.Contains(body, "```") {
		t.Fatalf("fences in heredoc body: %q", body)
	}
	if !strings.HasPrefix(strings.TrimSpace(body), "package store") {
		t.Fatalf("body=%q", body)
	}
	if strings.Contains(body, "```") {
		t.Fatalf("fences in body: %q", body)
	}
}

func TestRunOrchestratedCommand_heredocWritesGoWithoutFencesOrEOF(t *testing.T) {
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "mockrig", "mayor", "rig", "linkshelf", "internal", "store")
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		t.Fatal(err)
	}
	cmd := "export GT_ROOT=" + dir + " && cd mockrig/mayor/rig && cat > linkshelf/internal/store/store.go <<'EOF'\n" +
		"```go\npackage store\n\nimport \"fmt\"\n```\nEOF\nEOF"
	env := []string{"GT_ROOT=" + dir, "HOME=" + dir, "PATH=/usr/bin:/bin"}
	out, err := runOrchestratedCommand(cmd, dir, "", env, 0)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(storeDir, "store.go"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "```") {
		t.Fatalf("fences on disk: %q", s)
	}
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(line) == "EOF" {
			t.Fatalf("stray EOF line in file: %q", s)
		}
	}
	if !strings.HasPrefix(s, "package store") {
		t.Fatalf("got %q", s)
	}
}

func TestStateRunner_executeNativeEdit_editStripsJunkOnReplace(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	workDir := filepath.Join(dir, rig, "mayor", "rig")
	rel := "linkshelf/internal/store/store.go"
	abs := filepath.Join(workDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("package store\n\nfunc Old() {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	v := orchestrator.DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.RequiredFiles = []string{rel}
	v.BeadTitleContains = "Implement"
	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{NativeEditTools: true, CmdGuard: "implementation", Track: "implementation"},
		Validation: v,
	}
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-store"
	ops := []nativeEditOp{{
		kind:    "edit",
		path:    rel,
		search:  "func Old() {}",
		replace: "```go\nfunc New() {}\n```\nEOF\n",
	}}
	var combined strings.Builder
	r.executeNativeEdits(ops, rigMayorRigDir(dir, rig), "", nil, &combined)
	if r.track.hadCmdFailure {
		t.Fatalf("feedback:\n%s", combined.String())
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "```") || strings.Contains(s, "\nEOF\n") {
		t.Fatalf("junk in file: %q", s)
	}
	if !strings.Contains(s, "func New()") {
		t.Fatalf("replace not applied: %q", s)
	}
	if strings.Contains(s, "func Old()") {
		t.Fatalf("old func remains: %q", s)
	}
}

func TestParseOrchestratedNativeEdits_writeBodyGetsSanitizedOnDisk(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	workDir := filepath.Join(dir, rig, "mayor", "rig")
	rel := "linkshelf/internal/new/handler.go"
	abs := filepath.Join(workDir, filepath.FromSlash(rel))
	v := orchestrator.DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	v.RequiredFiles = []string{rel}
	v.BeadTitleContains = "Implement"
	task := &orchestrator.Task{
		State: "implementation",
		Hooks: orchestrator.StateHooks{NativeEditTools: true, CmdGuard: "implementation", Track: "implementation"},
		Validation: v,
	}
	r := newStateRunner(task, dir, rig)
	r.track.activeBead = "te-h"
	response := "WRITE: " + rel + "\n```go\npackage new\n\nfunc Handler() {}\n```\nEOF\n---END WRITE---\n"
	ops := parseOrchestratedNativeEdits(response)
	if len(ops) != 1 || ops[0].kind != "write" {
		t.Fatalf("ops=%+v", ops)
	}
	var combined strings.Builder
	r.executeNativeEdits(ops, rigMayorRigDir(dir, rig), "", nil, &combined)
	if r.track.hadCmdFailure {
		t.Fatalf("%s", combined.String())
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if strings.Contains(s, "```") || strings.TrimSpace(s) == "EOF" || strings.HasSuffix(strings.TrimSpace(s), "\nEOF") {
		t.Fatalf("sanitized write failed: %q", s)
	}
	if !strings.Contains(s, "func Handler()") {
		t.Fatalf("got %q", s)
	}
}

// heredocBodyForTest returns the first heredoc body between marker and a line containing only EOF.
func heredocBodyForTest(t *testing.T, script, marker string) string {
	t.Helper()
	i := strings.Index(script, marker)
	if i < 0 {
		t.Fatalf("missing %q in %q", marker, script)
	}
	rest := script[i+len(marker):]
	if strings.HasPrefix(rest, "\n") {
		rest = rest[1:]
	}
	var body []string
	for _, line := range strings.Split(rest, "\n") {
		if strings.TrimSpace(line) == "EOF" {
			break
		}
		body = append(body, line)
	}
	if len(body) == 0 && rest != "" {
		t.Fatalf("no heredoc body before EOF in %q", script)
	}
	return strings.Join(body, "\n")
}
