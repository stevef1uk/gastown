package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// IncrementalEditMinBytes is the on-disk size above which full-file heredoc overwrites are rejected.
const IncrementalEditMinBytes int64 = 120

var (
	implementCatPathRE = regexp.MustCompile(`(?i)cat\s*>\s*([^\s<;|&]+)`)
	// sed target is usually the last path-like token on the line.
	implementSedPathRE = regexp.MustCompile(`(?i)\bsed\b[^|;&]*\s+([^\s'";|&<>]+\.(?:go|py|js|mjs|cjs|ts|tsx|jsx|html|htm|css|scss|rs|java|rb|php|sh|sql|lua))\s*$`)
	implementPatchPathRE = regexp.MustCompile(`(?i)\bpatch\s+(?:-(?:[^\s]+\s+))*(?:<[^\s]+\s+)?([^\s;|&]+\.(?:go|py|js|ts|html|css))`)
)

// ExtractImplementWritePathFromCmd returns a repo-relative path under layout_root when cmd writes a source file.
func ExtractImplementWritePathFromCmd(cmd, layoutRoot string) string {
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	for _, re := range []*regexp.Regexp{implementCatPathRE, implementSedPathRE, implementPatchPathRE} {
		if p := normalizeImplementWritePath(re.FindStringSubmatch(cmd), layout); p != "" {
			return p
		}
	}
	// heredoc: cat > path <<'EOF' may match cat RE; also path before << without cat in some shells
	lower := strings.ToLower(cmd)
	if strings.Contains(lower, "<<") {
		if m := implementCatPathRE.FindStringSubmatch(cmd); len(m) >= 2 {
			return normalizeImplementWritePath(m, layout)
		}
	}
	return ""
}

func normalizeImplementWritePath(m []string, layout string) string {
	if len(m) < 2 {
		return ""
	}
	p := filepath.ToSlash(strings.Trim(m[1], `"'`))
	if layout != "" && !strings.HasPrefix(p, layout+"/") && strings.Contains(p, "/") {
		if idx := strings.Index(p, layout+"/"); idx >= 0 {
			p = p[idx:]
		}
	}
	return p
}

// IsImplementHeredocWrite reports a full-file overwrite via cat/heredoc (not sed/patch).
func IsImplementHeredocWrite(cmd string) bool {
	lower := strings.ToLower(cmd)
	if !strings.Contains(lower, "<<") && !strings.Contains(lower, "cat >") && !strings.Contains(lower, "cat>>") {
		return false
	}
	if strings.Contains(lower, "sed ") || strings.HasPrefix(strings.TrimSpace(lower), "patch ") {
		return false
	}
	return ExtractImplementWritePathFromCmd(cmd, "") != "" || strings.Contains(lower, "cat >")
}

// ImplementFileOnDisk returns size and whether the path exists under mayor/rig.
func ImplementFileOnDisk(townRoot, rig, relPath string) (size int64, exists bool, err error) {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" {
		return 0, false, nil
	}
	abs := filepath.Join(townRoot, rig, "mayor", "rig", filepath.FromSlash(relPath))
	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, false, err
	}
	if info.IsDir() {
		return 0, false, nil
	}
	return info.Size(), true, nil
}

// IsCmdMainImplementPath reports cmd/…/main.go entrypoints (wiring files; full rewrites are allowed).
func IsCmdMainImplementPath(relPath string) bool {
	p := strings.ToLower(filepath.ToSlash(strings.TrimSpace(relPath)))
	return strings.Contains(p, "/cmd/") && strings.HasSuffix(p, "/main.go")
}

// PreferIncrementalEdit reports whether agents should use sed/patch instead of heredoc rewrite.
func PreferIncrementalEdit(townRoot, rig, relPath string, v WorkflowValidation) bool {
	if IsCmdMainImplementPath(relPath) {
		return false
	}
	size, exists, err := ImplementFileOnDisk(townRoot, rig, relPath)
	if err != nil || !exists || size < IncrementalEditMinBytes {
		return false
	}
	abs := filepath.Join(townRoot, rig, "mayor", "rig", filepath.FromSlash(relPath))
	data, err := os.ReadFile(abs)
	if err != nil {
		return size >= IncrementalEditMinBytes
	}
	// CheckContentNotStub returns nil when the file is real work (not a stub/placeholder).
	if err := CheckContentNotStub(data, relPath, StubCheckOptionsFromValidation(v)); err == nil {
		return true
	}
	return false
}

// RejectFullFileHeredocReason returns a validation error message when cmd would rewrite an existing file.
func RejectFullFileHeredocReason(cmd, townRoot, rig, activeBead string, v WorkflowValidation) string {
	if !IsImplementHeredocWrite(cmd) {
		return ""
	}
	path := ExtractImplementWritePathFromCmd(cmd, v.LayoutRoot)
	if path == "" {
		return ""
	}
	if !PreferIncrementalEdit(townRoot, rig, path, v) {
		return ""
	}
	return fmt.Sprintf(
		"do not rewrite existing file %q with cat/heredoc or WRITE — use EDIT search/replace (or sed -i / patch); full rewrite risks truncation and syntax errors",
		path,
	)
}

// FormatIncrementalEditBlock returns prompt text when the active bead file already exists on disk.
func FormatIncrementalEditBlock(townRoot, rig, beadPath string, v WorkflowValidation) string {
	beadPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(beadPath)), v.LayoutRoot)
	if beadPath == "" || !PreferIncrementalEdit(townRoot, rig, beadPath, v) {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf(`### Incremental edit required
File **%s** already exists on disk (%d+ bytes). **Do not** use `+"`"+`cat > %s <<'EOF'`+"`"+` to replace the whole file.

Prefer small, targeted fixes:
- **Native tools (preferred):** `+"`"+`EDIT: %s`+"`"+` with `+"`"+`<<<<<<< SEARCH`+"`"+` / `+"`"+`=======`+"`"+` / `+"`"+`>>>>>>> REPLACE`+"`"+` blocks (see orchestrator context)
- **Shell fallback:** `+"`"+`sed -i`+"`"+` or `+"`"+`patch`+"`"+` if EDIT fails
- After edits, run **Verify** from Next bead, then `+"`"+`bd close`+"`"+`

Use full heredoc only when creating a **new** file (path missing on disk) or replacing a known stub/placeholder.`,
		beadPath, IncrementalEditMinBytes, beadPath, beadPath,
	))
}

// PathMatchesImplementWrite reports whether written path matches the allowed implement bead path.
func PathMatchesImplementWrite(written, allowed string, required []string) bool {
	written = filepath.ToSlash(strings.TrimSpace(written))
	allowed = filepath.ToSlash(strings.TrimSpace(allowed))
	if written == allowed {
		return true
	}
	for _, want := range required {
		want = filepath.ToSlash(strings.TrimSpace(want))
		if written == want && (allowed == want || filepath.Base(allowed) == filepath.Base(want)) {
			return true
		}
	}
	return filepath.Base(written) == filepath.Base(allowed)
}
