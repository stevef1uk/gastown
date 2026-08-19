package orchestrator

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Tester artifact filenames (written at rig root, i.e. {rig}/mayor/rig/).
const (
	TestPlanFileName   = "TEST_PLAN.md"
	TestReportFileName = "test-report.md"
)

var testPlanBlockRE = regexp.MustCompile(`(?m)^###\s+(\S.*)$`)

// EffectiveMinTestPlanBytes returns the minimum TEST_PLAN.md size for a rig.
func EffectiveMinTestPlanBytes(v WorkflowValidation) int64 {
	if v.MinTestPlanBytes > 0 {
		return v.MinTestPlanBytes
	}
	return DefaultMinTestPlanBytes
}

// TestPlanPath returns the TEST_PLAN.md path for a rig directory.
func TestPlanPath(rigDir string) string {
	return filepath.Join(rigDir, TestPlanFileName)
}

// TestReportPath returns the test-report.md path for a rig directory.
func TestReportPath(rigDir string) string {
	return filepath.Join(rigDir, TestReportFileName)
}

// TestPlanMeetsMinSize reports whether TEST_PLAN.md exists and is at least the
// profile's minimum size.
func TestPlanMeetsMinSize(rigDir string, v WorkflowValidation) bool {
	info, err := os.Stat(TestPlanPath(rigDir))
	if err != nil {
		return false
	}
	return info.Size() >= EffectiveMinTestPlanBytes(v)
}

// TestPlanRequirementIDs returns the `### <req-id>` headings in TEST_PLAN.md.
func TestPlanRequirementIDs(testPlan string) []string {
	var out []string
	for _, m := range testPlanBlockRE.FindAllStringSubmatch(testPlan, -1) {
		id := strings.TrimSpace(m[1])
		if id != "" {
			out = append(out, id)
		}
	}
	return out
}

// TestPlanBlock is one requirement row parsed from TEST_PLAN.md.
type TestPlanBlock struct {
	ReqID   string
	Level   string
	TestFile string
	BeadID  string
}

// ParseTestPlanBlocks parses `### <req-id>` blocks from TEST_PLAN.md.
// Lines are "Key: value"; multi-line Scenarios/Assertions lists are ignored
// for deterministic checks (they matter to the LLM judge, not the guard).
func ParseTestPlanBlocks(testPlan string) []TestPlanBlock {
	var blocks []TestPlanBlock
	var cur *TestPlanBlock
	sc := bufio.NewScanner(strings.NewReader(testPlan))
	for sc.Scan() {
		line := sc.Text()
		if m := testPlanBlockRE.FindStringSubmatch(line); m != nil {
			if cur != nil {
				blocks = append(blocks, *cur)
			}
			cur = &TestPlanBlock{ReqID: strings.TrimSpace(m[1])}
			continue
		}
		if cur == nil {
			continue
		}
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:idx]))
		val := strings.TrimSpace(line[idx+1:])
		switch key {
		case "level":
			cur.Level = val
		case "test file":
			cur.TestFile = val
		case "bead id":
			// Strip parenthetical descriptions (e.g. "(handler+tests)", "(go.mod)")
			// that the LLM may append after bead IDs — these are not valid bead IDs.
			if idx := strings.IndexAny(val, "([{"); idx >= 0 {
				val = strings.TrimSpace(val[:idx])
			}
			cur.BeadID = val
		}
	}
	if cur != nil {
		blocks = append(blocks, *cur)
	}
	return blocks
}

// PlannedTestFiles returns the "Test file:" paths named in TEST_PLAN.md.
func PlannedTestFiles(testPlan string) []string {
	var out []string
	for _, b := range ParseTestPlanBlocks(testPlan) {
		if f := strings.TrimSpace(b.TestFile); f != "" {
			// Strip parenthetical descriptions (e.g. "(via httptest, mux wiring check)",
			// "(verification via build)") that the LLM may append after test file paths —
			// these are not valid file paths.
			if idx := strings.IndexAny(f, "([{"); idx >= 0 {
				f = strings.TrimSpace(f[:idx])
			}
			out = append(out, f)
		}
	}
	return dedupeStrings(out)
}

// MissingPlannedTestFiles reports which planned test files do not exist on disk.
// Test files are resolved relative to the rig directory (paths may or may not
// carry the layout_root prefix; both are tried).
func MissingPlannedTestFiles(rigDir, layoutRoot string, testPlan string) []string {
	var missing []string
	for _, f := range PlannedTestFiles(testPlan) {
		if !plannedTestFileExists(rigDir, layoutRoot, f) {
			missing = append(missing, f)
		}
	}
	return missing
}

func plannedTestFileExists(rigDir, layoutRoot, f string) bool {
	f = filepath.ToSlash(strings.TrimSpace(f))
	if f == "" {
		return false
	}
	candidates := []string{f}
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	if layout != "" && layout != "." {
		if strings.HasPrefix(f, layout+"/") {
			candidates = append(candidates, strings.TrimPrefix(f, layout+"/"))
		} else {
			candidates = append(candidates, layout+"/"+strings.TrimPrefix(f, "/"))
		}
	}
	for _, c := range candidates {
		if info, err := os.Stat(filepath.Join(rigDir, filepath.FromSlash(c))); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

// StubTestFiles reports planned test files that exist but look like stubs
// (no substantive assertions/body). Uses CheckContentNotStub with the profile's
// minimums, relaxed so a focused unit test is not rejected for brevity.
func StubTestFiles(rigDir, layoutRoot string, v WorkflowValidation, testPlan string) []string {
	opts := StubCheckOptionsFromValidation(v)
	if opts.MinFileBytes > 160 {
		opts.MinFileBytes = 160
	}
	if opts.MinSubstantiveLines < 1 {
		opts.MinSubstantiveLines = 1
	}
	var stubs []string
	for _, f := range PlannedTestFiles(testPlan) {
		data, err := os.ReadFile(testFilePath(rigDir, layoutRoot, f))
		if err != nil {
			continue // missing files are reported separately
		}
		if err := CheckContentNotStub(data, f, opts); err != nil {
			stubs = append(stubs, f)
		}
	}
	return stubs
}

func testFilePath(rigDir, layoutRoot, f string) string {
	layout := strings.Trim(strings.TrimSpace(layoutRoot), "/")
	f = filepath.ToSlash(strings.TrimSpace(f))
	if layout != "" && layout != "." {
		if strings.HasPrefix(f, layout+"/") {
			return filepath.Join(rigDir, filepath.FromSlash(f))
		}
		return filepath.Join(rigDir, filepath.FromSlash(layout+"/"+f))
	}
	return filepath.Join(rigDir, filepath.FromSlash(f))
}

// MaxReviewRetries returns the tester's consecutive test_review retry cap.
func MaxReviewRetries(v WorkflowValidation) int {
	if v.MaxReviewRetries < 1 || v.MaxReviewRetries > 10 {
		return DefaultMaxReviewRetries
	}
	return v.MaxReviewRetries
}