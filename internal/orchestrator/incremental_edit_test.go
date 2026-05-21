package orchestrator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractImplementWritePathFromCmd_sed(t *testing.T) {
	cmd := `cd linkshelf && sed -i 's/st\.GetAll/st.GetAllLinks/g' linkshelf/internal/store/store.go`
	got := ExtractImplementWritePathFromCmd(cmd, "linkshelf")
	want := "linkshelf/internal/store/store.go"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestExtractImplementWritePathFromCmd_cat(t *testing.T) {
	cmd := `cat > linkshelf/cmd/server/main.go <<'EOF'`
	got := ExtractImplementWritePathFromCmd(cmd, "linkshelf")
	if got != "linkshelf/cmd/server/main.go" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractImplementWritePathFromCmd_patch(t *testing.T) {
	cmd := `cd linkshelf/mayor/rig && patch -p0 linkshelf/internal/store/store.go < fix.patch`
	got := ExtractImplementWritePathFromCmd(cmd, "linkshelf")
	want := "linkshelf/internal/store/store.go"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestPreferIncrementalEdit_substantiveFile(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	layout := "linkshelf"
	rel := layout + "/internal/store/store.go"
	absDir := filepath.Join(dir, rig, "mayor", "rig", layout, "internal", "store")
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "package store\n\n" + strings.Repeat("func (s *Store) GetAll() ([]Link, error) { return nil, nil }\n", 10)
	if err := os.WriteFile(filepath.Join(absDir, "store.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	v := DefaultWorkflowValidation()
	v.LayoutRoot = layout
	if !PreferIncrementalEdit(dir, rig, rel, v) {
		t.Fatal("substantive existing file should prefer incremental edit")
	}
}

func TestPreferIncrementalEdit_stubAllowsHeredoc(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	layout := "linkshelf"
	rel := layout + "/internal/store/store.go"
	absDir := filepath.Join(dir, rig, "mayor", "rig", layout, "internal", "store")
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(absDir, "store.go"), []byte("TODO: placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := DefaultWorkflowValidation()
	v.LayoutRoot = layout
	if PreferIncrementalEdit(dir, rig, rel, v) {
		t.Fatal("stub/placeholder file should allow full heredoc rewrite")
	}
}

func TestRejectFullFileHeredocReason_stubAllowsHeredoc(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	layout := "linkshelf"
	absDir := filepath.Join(dir, rig, "mayor", "rig", layout, "internal", "store")
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(absDir, "store.go"), []byte("placeholder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := DefaultWorkflowValidation()
	v.LayoutRoot = layout
	cmd := `cat > linkshelf/internal/store/store.go <<'EOF'
package store
func (s *Store) GetAll() ([]Link, error) { return nil, nil }
EOF`
	if reason := RejectFullFileHeredocReason(cmd, dir, rig, "te-1", v); reason != "" {
		t.Fatalf("stub file should allow heredoc, got %q", reason)
	}
}

func TestRejectFullFileHeredocReason_existingFile(t *testing.T) {
	dir := t.TempDir()
	rig := "mockrig"
	layout := "linkshelf"
	path := filepath.Join(dir, rig, "mayor", "rig", layout, "internal", "store")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "store.go")
	body := "package store\n\n" + strings.Repeat("type Link struct { ID int; URL string }\nfunc (s *Store) GetAll() ([]Link, error) { return nil, nil }\n", 8)
	if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	v := DefaultWorkflowValidation()
	v.LayoutRoot = layout
	v.RequiredFiles = []string{layout + "/internal/store/store.go"}
	cmd := `cd mockrig/mayor/rig && cat > linkshelf/internal/store/store.go <<'EOF'
package store
EOF`
	reason := RejectFullFileHeredocReason(cmd, dir, rig, "te-1", v)
	if reason == "" {
		t.Fatal("expected reject full heredoc on existing file")
	}
	if !strings.Contains(reason, "sed") {
		t.Fatalf("reason = %q", reason)
	}
}

func TestRejectFullFileHeredocReason_newFileAllowed(t *testing.T) {
	dir := t.TempDir()
	v := DefaultWorkflowValidation()
	v.LayoutRoot = "linkshelf"
	cmd := `cat > linkshelf/internal/newfile.go <<'EOF'
package newfile
EOF`
	if reason := RejectFullFileHeredocReason(cmd, dir, "mockrig", "te-1", v); reason != "" {
		t.Fatalf("new file should allow heredoc: %q", reason)
	}
}
