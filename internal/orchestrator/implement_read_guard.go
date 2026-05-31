package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ExtractImplementReadPathsFromCmd returns repo-relative paths from read-only shell commands.
func ExtractImplementReadPathsFromCmd(cmd, layoutRoot string) []string {
	layoutRoot = strings.Trim(strings.TrimSpace(layoutRoot), "/")
	var out []string
	for _, segment := range splitShellSegments(cmd) {
		if p := extractReadPathFromSegment(segment, layoutRoot); p != "" {
			out = appendUniqueString(out, p)
		}
	}
	return out
}

func splitShellSegments(cmd string) []string {
	cmd = strings.ReplaceAll(cmd, "\n", " ")
	var parts []string
	for _, chunk := range strings.Split(cmd, "&&") {
		for _, piece := range strings.Split(chunk, ";") {
			if s := strings.TrimSpace(piece); s != "" {
				parts = append(parts, s)
			}
		}
	}
	return parts
}

func extractReadPathFromSegment(segment, layoutRoot string) string {
	lower := strings.ToLower(strings.TrimSpace(segment))
	for _, prefix := range []string{"cat ", "head ", "tail ", "less ", "more "} {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		if prefix == "cat " && (strings.HasPrefix(lower, "cat >") || strings.HasPrefix(lower, "cat>>")) {
			return ""
		}
		rest := strings.TrimSpace(segment[len(prefix):])
		if rest == "" {
			return ""
		}
		// First path token (stop at flags, pipes, redirects).
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return ""
		}
		return normalizeImplementReadPath(fields[0], layoutRoot)
	}
	return ""
}

func normalizeImplementReadPath(raw, layoutRoot string) string {
	raw = filepath.ToSlash(strings.Trim(raw, `"'`))
	if raw == "" || strings.Contains(raw, "..") {
		return ""
	}
	layoutRoot = strings.Trim(layoutRoot, "/")
	if layoutRoot != "" && !strings.HasPrefix(raw, layoutRoot+"/") {
		if idx := strings.Index(raw, layoutRoot+"/"); idx >= 0 {
			raw = raw[idx:]
		} else if !strings.Contains(raw, "/") && strings.HasSuffix(raw, ".go") {
			// bare filename under layout — skip ambiguous reads
			return ""
		}
	}
	return raw
}

func isImplementPlanningDocPath(relPath string) bool {
	switch strings.ToLower(filepath.Base(relPath)) {
	case "spec.md", "architecture.md", "plan.md", "readme.md", "agents.md":
		return true
	default:
		return false
	}
}

// ValidateImplementReadMissingFile rejects cat/READ of absent implement paths with actionable guidance.
// activeBeadPath may be set by gt-agent when the in_progress bead path is known (avoids extra bd lookups).
func ValidateImplementReadMissingFile(townRoot, rig, activeBead, activeBeadPath, relPath string, v WorkflowValidation) error {
	relPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(relPath)), v.LayoutRoot)
	if relPath == "" {
		return nil
	}
	if isImplementPlanningDocPath(relPath) {
		return nil
	}
	if _, exists, err := ImplementFileOnDisk(townRoot, rig, relPath); err != nil || exists {
		return err
	}
	return fmt.Errorf("%s", ImplementMissingFileReadNudge(townRoot, rig, activeBead, activeBeadPath, relPath, v))
}

// ImplementMissingFileReadNudge explains why a missing-path read failed and what to do instead.
func ImplementMissingFileReadNudge(townRoot, rig, activeBead, activeBeadPath, relPath string, v WorkflowValidation) string {
	relPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(relPath)), v.LayoutRoot)
	activePath := NormalizeBeadPathForLayout(strings.TrimSpace(activeBeadPath), v.LayoutRoot)
	if activePath == "" {
		activePath = NormalizeBeadPathForLayout(ImplementBeadPathForID(townRoot, rig, activeBead, v), v.LayoutRoot)
	}
	var b strings.Builder
	b.WriteString("cannot read `")
	b.WriteString(relPath)
	b.WriteString("` — file does not exist on disk yet.\n")

	if activePath != "" && PathMatchesImplementWrite(relPath, activePath, v.RequiredFiles) {
		b.WriteString("This path is the **active implement bead** — use **WRITE:** to create it (or **EDIT:** after it exists), not `cat`.\n")
		return strings.TrimSpace(b.String())
	}

	if WorkflowUsesGo(v) && strings.HasSuffix(relPath, "_test.go") {
		if activePath != "" && !IsTestImplementPath(activePath) {
			testBead := CorrelatedTestPathForSource(activePath, v)
			if testBead != "" && PathMatchesImplementWrite(relPath, testBead, v.RequiredFiles) {
				b.WriteString("Unit tests live on a **separate implement bead** (`")
				b.WriteString(testBead)
				b.WriteString("`). Finish production code on `")
				b.WriteString(activePath)
				b.WriteString("` with **go build** Verify, then `bd close` and start the test bead.\n")
				return strings.TrimSpace(b.String())
			}
		}
	}

	if IsTestImplementPath(relPath) {
		b.WriteString("Use **WRITE:** on this test bead to add `*_test.go` / `tests/test_*.py` content mapped to plan.md acceptance, then run Verify (`go test` / `pytest`).\n")
		return strings.TrimSpace(b.String())
	}

	if next, err := NextOpenImplementBead(townRoot, rig, v); err == nil && next != nil {
		nextPath := NormalizeBeadPathForLayout(
			ExtractPathFromBeadTitle(next.Title, v.BeadTitleContains),
			v.LayoutRoot,
		)
		if nextPath != "" && PathMatchesImplementWrite(relPath, nextPath, v.RequiredFiles) {
			b.WriteString("Finish the current bead first — **Next bead** is `")
			b.WriteString(nextPath)
			b.WriteString("` (")
			b.WriteString(next.ID)
			b.WriteString(").\n")
			return strings.TrimSpace(b.String())
		}
	}

	b.WriteString("Use **WRITE:** / **EDIT:** for implement files under required_files, or read dependency packages shown in Implement context.\n")
	return strings.TrimSpace(b.String())
}

// mayorRigAbsPath returns the absolute path under mayor/rig for a relative path.
func mayorRigAbsPath(townRoot, rig, relPath string) string {
	return filepath.Join(townRoot, rig, "mayor", "rig", filepath.FromSlash(relPath))
}

// EnsureTestBeadSkeleton creates a minimal legal test file when a test implement bead opens.
func EnsureTestBeadSkeleton(townRoot, rig, beadID string, v WorkflowValidation) (string, bool, error) {
	beadID = strings.TrimSpace(beadID)
	if beadID == "" {
		return "", false, nil
	}
	beadPath := NormalizeBeadPathForLayout(ImplementBeadPathForID(townRoot, rig, beadID, v), v.LayoutRoot)
	return ensureTestBeadSkeletonForPath(townRoot, rig, beadPath, v)
}

// EnsureTestBeadSkeletonForPath creates a skeleton at beadPath when it is a missing test file.
func EnsureTestBeadSkeletonForPath(townRoot, rig, beadPath string, v WorkflowValidation) (string, bool, error) {
	beadPath = NormalizeBeadPathForLayout(filepath.ToSlash(strings.TrimSpace(beadPath)), v.LayoutRoot)
	return ensureTestBeadSkeletonForPath(townRoot, rig, beadPath, v)
}

func ensureTestBeadSkeletonForPath(townRoot, rig, beadPath string, v WorkflowValidation) (string, bool, error) {
	if beadPath == "" || !IsTestImplementPath(beadPath) {
		return "", false, nil
	}
	abs := mayorRigAbsPath(townRoot, rig, beadPath)
	if _, err := os.Stat(abs); err == nil {
		return beadPath, false, nil
	} else if !os.IsNotExist(err) {
		return "", false, err
	}
	content := testBeadSkeletonContent(beadPath, v)
	if content == "" {
		return "", false, nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return "", false, err
	}
	if err := os.WriteFile(abs, []byte(content), 0644); err != nil {
		return "", false, err
	}
	return beadPath, true, nil
}

func testBeadSkeletonContent(beadPath string, v WorkflowValidation) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if beadPath == "" {
		return ""
	}
	if WorkflowUsesGo(v) && strings.HasSuffix(beadPath, "_test.go") {
		pkg := filepath.Base(filepath.Dir(beadPath))
		if pkg == "." || pkg == "/" {
			pkg = "main"
		}
		return "package " + pkg + "\n\nimport \"testing\"\n\n// Replace with table-driven tests from plan.md acceptance.\nfunc TestPlaceholder(t *testing.T) {\n\tt.Skip(\"implement tests per plan.md acceptance\")\n}\n"
	}
	if WorkflowUsesPython(v) && IsTestImplementPath(beadPath) {
		return "import pytest\n\n\n@pytest.mark.skip(reason=\"implement tests per plan.md acceptance\")\ndef test_placeholder():\n    pass\n"
	}
	return ""
}
