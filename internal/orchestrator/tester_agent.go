package orchestrator

import (
	"bufio"
	"fmt"
	"log"
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

// PhaseShipsAutomatedTests reports whether a delivery phase runs automated tests.
// A setup-only phase (manifest/config files such as requirements.txt, go.mod,
// package.json; verify command that explicitly says there are no automated tests)
// has no test file and therefore must NOT get a `### <phase-id>` block in
// TEST_PLAN.md. This is the code-side twin of the test_plan.md hard rule
// ("omit the section for setup-only phases").
func PhaseShipsAutomatedTests(p DeliveryPhase) bool {
	cmd := strings.ToLower(strings.TrimSpace(p.QAVerifyCommand))
	if cmd == "" {
		return true // unknown verify — be conservative and keep the phase testable
	}
	if strings.Contains(cmd, "no automated tests") {
		return false
	}
	if strings.Contains(cmd, "echo") && !hasAnyStackTool(cmd) {
		return false // "verify ok (no automated tests for this phase)" style stub
	}
	return true
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
	ReqID    string
	Level    string
	TestFile string
	BeadID   string
	Phase    string
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
		case "phase":
			cur.Phase = val
		case "test file":
			// Handle comma-separated test files (e.g., "file1.go, file2.go")
			if strings.Contains(val, ",") {
				// Split by comma and clean each path
				parts := strings.Split(val, ",")
				for i, part := range parts {
					parts[i] = strings.TrimSpace(part)
					// Strip parenthetical descriptions from each part
					if idx := strings.IndexAny(parts[i], "([{"); idx >= 0 {
						parts[i] = strings.TrimSpace(parts[i][:idx])
					}
				}
				// For each additional file, create a new block
				for i := 1; i < len(parts); i++ {
					if parts[i] != "" {
						newBlock := *cur
						newBlock.TestFile = parts[i]
						blocks = append(blocks, newBlock)
					}
				}
				// Set the first file for the current block
				if len(parts) > 0 {
					cur.TestFile = parts[0]
				}
			} else {
				cur.TestFile = val
			}
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

// PlannedTestFilesForPhases returns test file paths for blocks whose Phase matches
// one of the given phase IDs, plus all blocks with no Phase field (backward compatible).
func PlannedTestFilesForPhases(testPlan string, phaseIDs []string) []string {
	if len(phaseIDs) == 0 {
		return PlannedTestFiles(testPlan)
	}
	phaseSet := make(map[string]bool, len(phaseIDs))
	for _, p := range phaseIDs {
		phaseSet[strings.ToLower(strings.TrimSpace(p))] = true
	}
	var out []string
	for _, b := range ParseTestPlanBlocks(testPlan) {
		// Include blocks with no Phase (backward compatible) or matching phase
		if b.Phase == "" || phaseSet[strings.ToLower(strings.TrimSpace(b.Phase))] {
			if f := strings.TrimSpace(b.TestFile); f != "" {
				if idx := strings.IndexAny(f, "([{"); idx >= 0 {
					f = strings.TrimSpace(f[:idx])
				}
				out = append(out, f)
			}
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

// MissingPlannedTestFilesForPhases reports which planned test files do not exist on disk,
// filtered by the given phase IDs. Blocks with no Phase field are always included.
func MissingPlannedTestFilesForPhases(rigDir, layoutRoot string, testPlan string, phaseIDs []string) []string {
	var missing []string
	for _, f := range PlannedTestFilesForPhases(testPlan, phaseIDs) {
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

// StubTestFilesForPhases reports planned test files that exist but look like stubs,
// filtered by the given phase IDs. Blocks with no Phase field are always included.
func StubTestFilesForPhases(rigDir, layoutRoot string, v WorkflowValidation, testPlan string, phaseIDs []string) []string {
	opts := StubCheckOptionsFromValidation(v)
	if opts.MinFileBytes > 160 {
		opts.MinFileBytes = 160
	}
	if opts.MinSubstantiveLines < 1 {
		opts.MinSubstantiveLines = 1
	}
	var stubs []string
	for _, f := range PlannedTestFilesForPhases(testPlan, phaseIDs) {
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

// TestPlanBeadMappingMismatches reports TEST_PLAN.md rows whose Bead ID exists
// but whose bead title does not reference the row's Test file path. A row like
// "Test file: internal/api/handlers_test.go / Bead ID: te-7o7" where te-7o7 is
// titled "Implement internal/api/handlers.go" sends rework to the wrong bead —
// the cited-ID reopen path then reopens a bead that never owned the stub.
func TestPlanBeadMappingMismatches(townRoot, rig string, v WorkflowValidation, blocks []TestPlanBlock) ([]string, error) {
	titles, err := RigBeadTitlesByID(townRoot, rig)
	if err != nil {
		return nil, err
	}
	return testPlanBeadMappingMismatches(v, blocks, titles), nil
}

func testPlanBeadMappingMismatches(v WorkflowValidation, blocks []TestPlanBlock, titles map[string]string) []string {
	var bad []string
	for _, b := range blocks {
		beadID := strings.ToLower(strings.TrimSpace(b.BeadID))
		testFile := filepath.ToSlash(strings.TrimSpace(b.TestFile))
		if beadID == "" || testFile == "" {
			continue
		}
		title := strings.TrimSpace(titles[beadID])
		if title == "" {
			continue // unknown IDs are reported by bead-ID existence checks
		}
		got := NormalizeBeadPathForLayout(ExtractPathFromBeadTitle(title, v.BeadTitleContains), v.LayoutRoot)
		if got == "" || PathMatchesImplementFile(testFile, got) {
			continue
		}
		bad = append(bad, fmt.Sprintf("row %s cites %s but that bead owns %q (plan says %q)", b.ReqID, b.BeadID, got, testFile))
	}
	return bad
}

// HallucinatedTestPlanRequirements returns requirement IDs in TEST_PLAN.md that
// do not appear in SPEC.md or architecture.md. This catches LLM hallucinations
// where the tester invents requirements not defined in the source documents.
// deliveryPhaseIDs are the workflow-profile delivery phase IDs, which are always
// valid requirement IDs in TEST_PLAN.md (even if they don't appear in SPEC/arch).
func HallucinatedTestPlanRequirements(testPlan, specDoc, archDoc string, deliveryPhaseIDs ...string) []string {
	// Collect requirement IDs from SPEC.md and architecture.md
	validIDs := make(map[string]bool)
	doc := specDoc + "\n" + archDoc

	// 1. Collect explicit ### <id> headings
	for _, m := range testPlanBlockRE.FindAllStringSubmatch(doc, -1) {
		id := strings.TrimSpace(m[1])
		if id != "" {
			validIDs[strings.ToUpper(id)] = true
		}
	}

	// 2. Also extract route-based requirement IDs from HTTP API tables.
	//    e.g. "| GET | /api/links |" implies requirements for that route.
	//    These become route-style IDs like "GET /api/links" that the tester
	//    can reference instead of hallucinated REQ-N IDs.
	routeRE := regexp.MustCompile(`(?i)\|\s*(GET|POST|PUT|DELETE|PATCH)\s*\|\s*` + "`?([^`|]+)`?" + `\s*\|`)
	for _, m := range routeRE.FindAllStringSubmatch(doc, -1) {
		if len(m) >= 3 {
			method := strings.ToUpper(strings.TrimSpace(m[1]))
			path := strings.TrimSpace(m[2])
			routeID := method + " " + path
			validIDs[strings.ToUpper(routeID)] = true
		}
	}

	// 3. Also extract route-based requirement IDs from bullet-point format.
	//    e.g. "- GET /ping → 200 JSON" or "- POST /api/users → 201"
	bulletRouteRE := regexp.MustCompile(`(?im)^[-*]\s*(GET|POST|PUT|DELETE|PATCH)\s+(/[^\s→,;]+)`)
	for _, m := range bulletRouteRE.FindAllStringSubmatch(doc, -1) {
		if len(m) >= 3 {
			method := strings.ToUpper(strings.TrimSpace(m[1]))
			path := strings.TrimSpace(m[2])
			routeID := method + " " + path
			validIDs[strings.ToUpper(routeID)] = true
		}
	}

	// 4. Delivery phase IDs are always valid — they come from the workflow
	//    profile and the tester MUST create blocks for every phase.
	for _, pid := range deliveryPhaseIDs {
		validIDs[strings.ToUpper(strings.TrimSpace(pid))] = true
	}

	// If no valid IDs found in source documents, skip the check
	// (SPEC may use a different format that we don't parse).
	if len(validIDs) == 0 {
		return nil
	}

	// Check TEST_PLAN.md blocks
	var hallucinated []string
	for _, b := range ParseTestPlanBlocks(testPlan) {
		id := strings.TrimSpace(b.ReqID)
		if id == "" {
			continue
		}
		if !validIDs[strings.ToUpper(id)] {
			hallucinated = append(hallucinated, id)
		}
	}
	return hallucinated
}

// HasRequirementHeadingsForIDs reports whether doc contains a `### <id>`
// heading for EVERY given requirement ID (case-insensitive). Matching against
// the actual delivery-phase IDs prevents false positives where an unrelated
// heading (e.g. "### HTTP API Table") satisfies the check while the Tester
// still has no valid IDs to anchor TEST_PLAN.md blocks to.
func HasRequirementHeadingsForIDs(doc string, ids []string) bool {
	if len(ids) == 0 {
		return true
	}
	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[strings.ToLower(strings.TrimSpace(id))] = struct{}{}
	}
	for _, line := range strings.Split(doc, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "### ") {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "### ")))
		if _, ok := wanted[id]; ok {
			delete(wanted, id)
			if len(wanted) == 0 {
				return true
			}
		}
	}
	return len(wanted) == 0
}

// validateTestPlan validates that TEST_PLAN.md aligns with workflow-profile.json
// and SPEC.md/architecture.md. Called from manager.go when tester reports success
// from test_plan state. Returns error if validation fails.
func validateTestPlan(townRoot, rig string) error {
	// Load workflow-profile.json WITHOUT clamping (to get original required_files before test plan merge)
	v, _, err := LoadRawRigWorkflowProfile(townRoot, rig)
	if err != nil {
		return fmt.Errorf("load workflow profile: %w", err)
	}

	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	testPlanPath := TestPlanPath(rigDir)

	testPlanBytes, err := os.ReadFile(testPlanPath)
	if err != nil {
		return fmt.Errorf("TEST_PLAN.md not found at %s: %w", testPlanPath, err)
	}
	testPlan := string(testPlanBytes)

	// Check 1: Hallucinated requirement IDs (section headings must match delivery phase IDs)
	deliveryPhaseIDs := make([]string, len(v.DeliveryPhases))
	for i, p := range v.DeliveryPhases {
		deliveryPhaseIDs[i] = p.ID
	}

	// Read SPEC.md and architecture.md for hallucination check
	specPath := filepath.Join(rigDir, "SPEC.md")
	archPath := filepath.Join(rigDir, "architecture.md")
	specDoc, _ := os.ReadFile(specPath)
	archDoc, _ := os.ReadFile(archPath)

	hallucinated := HallucinatedTestPlanRequirements(testPlan, string(specDoc), string(archDoc), deliveryPhaseIDs...)
	if len(hallucinated) > 0 {
		return fmt.Errorf("TEST_PLAN.md has invalid section IDs (not in SPEC/architecture/delivery phases): %v", hallucinated)
	}

	// Check 2: Test file paths must exist in workflow-profile.json required_files
	blocks := ParseTestPlanBlocks(testPlan)
	validTestFiles := make(map[string]bool)
	// Collect all required_files from workflow-profile.json (global + per-phase)
	for _, f := range v.RequiredFiles {
		validTestFiles[strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))] = true
	}
	for _, phase := range v.DeliveryPhases {
		for _, f := range phase.RequiredFiles {
			validTestFiles[strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))] = true
		}
	}

	for _, b := range blocks {
		testFile := strings.TrimSpace(b.TestFile)
		if testFile == "" || IsPlanGapPlaceholderPath(testFile) {
			continue
		}
		// Strip parenthetical descriptions
		if idx := strings.IndexAny(testFile, "([{"); idx >= 0 {
			testFile = strings.TrimSpace(testFile[:idx])
		}
		testFile = filepath.ToSlash(testFile)
		key := strings.ToLower(testFile)
		if !validTestFiles[key] {
			return fmt.Errorf("TEST_PLAN.md test file %q not in workflow-profile.json required_files", testFile)
		}
	}

	// Check 3: Bead IDs must match actual beads (if beads database exists)
	mismatches, err := TestPlanBeadMappingMismatches(townRoot, rig, v, blocks)
	if err != nil {
		// Beads database not found - skip bead mapping check but log warning
		log.Printf("[validateTestPlan] skipping bead mapping check (no beads database): %v", err)
	} else if len(mismatches) > 0 {
		return fmt.Errorf("TEST_PLAN.md bead mapping errors: %v", mismatches)
	}

	return nil
}