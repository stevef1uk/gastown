package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var nilSliceVarRE = regexp.MustCompile(`(?m)var\s+(\w+)\s+(\[\]\S+)\s*$`)

// GoListLikelyReturnsNilSlice reports whether src looks like List() returns a nil slice (fails Test*_Empty).
func GoListLikelyReturnsNilSlice(src string) bool {
	m := nilSliceVarRE.FindStringSubmatch(src)
	if len(m) < 2 {
		return false
	}
	name := m[1]
	return strings.Contains(src, "return "+name+", nil") &&
		!strings.Contains(src, "make([]") &&
		!strings.Contains(src, name+" := make(")
}

// AllowClosedDepFixForVerifyFailure allows editing a closed earlier production file when the active
// bead is a unit-test file and package tests fail because List (or similar) returns nil not [].
func AllowClosedDepFixForVerifyFailure(townRoot, rig, activeBeadPath, writtenPath, verifyOutput string, v WorkflowValidation) bool {
	activeBeadPath = filepath.ToSlash(strings.TrimSpace(activeBeadPath))
	writtenPath = filepath.ToSlash(strings.TrimSpace(writtenPath))
	if activeBeadPath == "" || writtenPath == "" || !WorkflowUsesGo(v) || !IsTestImplementPath(activeBeadPath) {
		return false
	}
	if IsTestImplementPath(writtenPath) {
		return false
	}
	if GoBuildRelPackage(v.LayoutRoot, activeBeadPath) != GoBuildRelPackage(v.LayoutRoot, writtenPath) {
		return false
	}
	closedOnly, err := ImplementPathHasOnlyClosedBeads(townRoot, rig, writtenPath, v)
	if err != nil || !closedOnly {
		return false
	}
	abs := filepath.Join(townRoot, rig, "mayor", "rig", filepath.FromSlash(writtenPath))
	data, err := os.ReadFile(abs)
	if err != nil {
		return false
	}
	if !GoListLikelyReturnsNilSlice(string(data)) {
		return false
	}
	if out := strings.TrimSpace(verifyOutput); out != "" {
		return strings.Contains(out, "nil slice, want empty slice") ||
			strings.Contains(out, "nil slice, want empty") ||
			goTestOutputSuggestsFailure(out)
	}
	return true
}

// FormatNilSliceListUnblockHint returns concrete EDIT guidance when store_test (or similar) is blocked
// by a closed production bead whose List returns nil instead of an empty slice.
func FormatNilSliceListUnblockHint(townRoot, rig, activeBeadPath string, v WorkflowValidation) string {
	if !WorkflowUsesGo(v) || !IsTestImplementPath(activeBeadPath) {
		return ""
	}
	pkg := GoBuildRelPackage(v.LayoutRoot, activeBeadPath)
	if pkg == "" {
		return ""
	}
	for _, want := range v.RequiredFiles {
		want = filepath.ToSlash(strings.TrimSpace(want))
		if want == "" || IsTestImplementPath(want) {
			continue
		}
		if GoBuildRelPackage(v.LayoutRoot, want) != pkg {
			continue
		}
		abs := filepath.Join(townRoot, rig, "mayor", "rig", filepath.FromSlash(want))
		data, err := os.ReadFile(abs)
		if err != nil || !GoListLikelyReturnsNilSlice(string(data)) {
			continue
		}
		id, closed := ClosedImplementBeadForPath(townRoot, rig, want, v)
		if !closed {
			continue
		}
		m := nilSliceVarRE.FindStringSubmatch(string(data))
		varName := "links"
		if len(m) >= 2 {
			varName = m[1]
		}
		return strings.TrimSpace(fmt.Sprintf(`### Unblock: fix closed production file (required for this test bead)
Package tests fail because **`+"`%s`"+`** returns a **nil** slice; tests want **`+"`[]`"+`** (empty, non-nil).

You may **EDIT:** **`+"`%s`"+`** even though bead **%s** is closed — this is a verify-driven fix for the active test bead.

In **`+"`List`"+`**, change:
`+"`var %s []...`"+` → **`+"`%s := make([]..., 0)`"+`** (or `+"`return []T{}, nil`"+` when the slice is empty), then re-run **Verify**.

Example **EDIT:** **`+"`%s`"+`**:
`+"`<<<<<<< SEARCH`"+`
var %s []Link
`+"`=======`"+`
%s := make([]Link, 0)
`+"`>>>>>>> REPLACE`"+`

Then **Verify** (`+"`go test`"+` on this package), then continue **`+"`bd close`"+`** on the active test bead.`,
			want, want, id, varName, varName, want, varName, varName))
	}
	return ""
}
