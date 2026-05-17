// Package testrig provides generic rig names and paths for unit tests only (not production rigs).
package testrig

const (
	// Name is the default rig identifier in tests.
	Name = "mockrig"
	// Alt is a second rig name when tests need two rigs.
	Alt = "mockrigb"
	// LayoutRoot is a generic profile layout_root (never a real product name).
	LayoutRoot = "myapp"
	// RequirementsFile is a generic requirements path under LayoutRoot.
	RequirementsFile = LayoutRoot + "/pkg/requirements.txt"
	// UnittestModule is a generic unittest module path for validation tests.
	UnittestModule = LayoutRoot + ".pkg.test_api"
)

// Worktree returns the mayor/rig path prefix for a rig (e.g. mockrig/mayor/rig).
func Worktree(rig string) string {
	if rig == "" {
		rig = Name
	}
	return rig + "/mayor/rig"
}
