package orchestrator

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type testReviewPlanGap struct{ reason string }

func (e *testReviewPlanGap) Error() string { return e.reason }

type testReviewRepair struct {
	phase    string
	beads    []PlanBead
	tests    map[string][]string
	deferred map[string]map[string][]string
}

func classifyTestReviewFinding(summary, test, testPath string) (bool, error) {
	named := strings.Contains(summary, test) || strings.Contains(summary, testPath)
	testRepair := strings.Contains(summary, "Test repair: "+test) || strings.Contains(summary, "Test repair: "+testPath)
	implementationRepair := strings.Contains(summary, "Implementation repair: "+test) || strings.Contains(summary, "Implementation repair: "+testPath)
	if named && testRepair == implementationRepair {
		return false, fmt.Errorf("classify the finding for %s in the summary as exactly one of 'Test repair: %s' or 'Implementation repair: %s'", testPath, testPath, testPath)
	}
	return testRepair, nil
}

// ValidateContractMismatchSummary requires an auditable SPEC-to-assertion-to-source
// trace before a tester can send implementation work back as a contract mismatch.
func ValidateContractMismatchSummary(summary string) error {
	for _, marker := range []string{"Implementation repair:", "SPEC:", "Assertion:", "Production fix:"} {
		if !strings.Contains(summary, marker) {
			return fmt.Errorf("contract_mismatch summary must include %q with the exact evidence; got %q", marker, strings.TrimSpace(summary))
		}
	}
	return nil
}

// sortedScopeKeys returns sorted keys for consistent error messages.
func sortedScopeKeys(scope map[string]bool) []string {
	keys := make([]string, 0, len(scope))
	for k := range scope {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// ValidateTestReviewRepairSummary lets the agent correct an ambiguous report
// before sending it to the authoritative orchestrator.
func ValidateTestReviewRepairSummary(rigDir string, v WorkflowValidation, summary string) error {
	data, err := os.ReadFile(TestPlanPath(rigDir))
	if err != nil {
		return fmt.Errorf("read test plan to classify review findings: %w", err)
	}
	scope := testPlanPhaseScope(v)
	for _, block := range ParseTestPlanBlocks(string(data)) {
		phase := strings.ToLower(strings.TrimSpace(block.Phase))
		test := cleanPlannedTestFilePath(block.TestFile)
		if test == "" {
			continue
		}
		testPath := NormalizePlannerBeadPath(test, v.LayoutRoot, "")
		if scope != nil && phase != "" && !scope[phase] {
			// Future-phase test row: must NOT be reported as failing or marked for repair.
			if strings.Contains(summary, test) || strings.Contains(summary, testPath) {
				lowerSummary := strings.ToLower(summary)
				isDeferredMention := strings.Contains(lowerSummary, "deferred") || strings.Contains(lowerSummary, "out of scope") || strings.Contains(lowerSummary, "future")
				testRepair := strings.Contains(summary, "Test repair: "+test) || strings.Contains(summary, "Test repair: "+testPath)
				implementationRepair := strings.Contains(summary, "Implementation repair: "+test) || strings.Contains(summary, "Implementation repair: "+testPath)
				if testRepair || implementationRepair || !isDeferredMention {
					return fmt.Errorf("test %s belongs to future phase %q (active phase is %q, in scope: %s). Future-phase tests are deferred until that phase begins and must not be marked for repair or cause review failure. If in-scope tests pass and phase verify is green, return outcome \"success\".", testPath, block.Phase, v.ActivePhaseID(), strings.Join(sortedScopeKeys(scope), ", "))
				}
			}
			continue
		}
		if _, err := classifyTestReviewFinding(summary, test, testPath); err != nil {
			return err
		}
	}
	return nil
}

func isBeadExplicitlyCitedForRepair(summary, beadID string) bool {
	if beadID == "" {
		return false
	}
	lower := strings.ToLower(summary)
	lowerID := strings.ToLower(beadID)
	if !strings.Contains(lower, lowerID) {
		return false
	}
	for _, marker := range []string{
		"reopen " + lowerID,
		"reopen bead " + lowerID,
		"repair " + lowerID,
		"repair bead " + lowerID,
		"fix " + lowerID,
		"reopened " + lowerID,
		"reopened bead " + lowerID,
		"bead " + lowerID + " lacks",
		"bead " + lowerID + " missing",
		"bead " + lowerID + " failed",
		"bead " + lowerID + " fails",
		"bead " + lowerID + " broken",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func selectTestReviewRepair(rigDir string, v WorkflowValidation, plan, summary string, beads []PlanBead) (testReviewRepair, error) {
	scope := testPlanPhaseScope(v)
	var blocks []TestPlanBlock
	for _, block := range ParseTestPlanBlocks(plan) {
		phase := strings.ToLower(strings.TrimSpace(block.Phase))
		if scope == nil || phase == "" || scope[phase] {
			blocks = append(blocks, block)
		}
	}
	gap := func(reason string) (testReviewRepair, error) {
		return testReviewRepair{}, &testReviewPlanGap{reason: reason}
	}
	if len(blocks) == 0 {
		return gap("TEST_PLAN.md has no in-scope requirement rows with repair ownership")
	}
	if err := ValidateTestPlanPaths(v, blocks); err != nil {
		return gap(err.Error())
	}
	byPath := map[string][]PlanBead{}
	known := map[string]bool{}
	prefixes := map[string]bool{}
	for _, bead := range beads {
		path, _, _ := strings.Cut(ExtractPathFromBeadTitle(bead.Title, v.BeadTitleContains), " per ")
		path = NormalizePlannerBeadPath(NormalizeBeadPathForLayout(path, v.LayoutRoot), v.LayoutRoot, "")
		byPath[path] = append(byPath[path], bead)
		known[bead.ID] = true
		prefixes[strings.SplitN(bead.ID, "-", 2)[0]] = true
	}
	cited := map[string]bool{}
	for prefix := range prefixes {
		for _, id := range ExtractKnownRigBeadIDsFromSummary(summary, prefix, known) {
			cited[id] = true
		}
	}
	broken := map[string]bool{}
	for _, path := range append(MissingPlannedTestFiles(rigDir, v.LayoutRoot, plan), StubTestFiles(rigDir, v.LayoutRoot, v, plan)...) {
		broken[path] = true
	}
	type candidate struct {
		phase          string
		bead           PlanBead
		test           string
		named          bool
		explicit       bool
		reported       bool
		citedInSummary bool
	}
	type testOwner struct{ test, beadID string }
	var candidates []candidate
	anyNamed := false
	activeNamedTests := map[testOwner]bool{}
	for _, block := range blocks {
		test := cleanPlannedTestFilePath(block.TestFile)
		phaseV := v
		phase := strings.TrimSpace(block.Phase)
		if phase == "" {
			phaseV.RequiredFiles = v.UnionRequiredFiles()
			phaseV.DeliveryPhases = nil
		} else {
			for _, declared := range v.DeliveryPhases {
				if strings.EqualFold(phase, declared.ID) {
					phase = declared.ID
					break
				}
			}
			phaseV.ActivePhaseIDField = phase
		}
		path := NormalizePlannerBeadPath(test, v.LayoutRoot, "")
		testPath := path
		owners := byPath[path]
		if len(owners) == 0 {
			path = SourcePathForOwnedTest(path, phaseV)
			owners = byPath[path]
		}
		if len(owners) != 1 {
			// When the review specifically targets other tests, do not block repair
			// because an un-cited, unbroken test from an earlier completed phase lacks
			// an implementation bead in the database.
			hasExplicitRepair := strings.Contains(summary, "Test repair:") || strings.Contains(summary, "Implementation repair:")
			reported := strings.Contains(summary, test) || strings.Contains(summary, path) || strings.Contains(summary, testPath)
			if hasExplicitRepair && !reported && !broken[test] && v.HasPhasedDelivery() && v.IsPhaseCompleted(phase) && phase != v.ActivePhaseID() {
				continue
			}
			return gap(fmt.Sprintf("TEST_PLAN.md row %s test %s has no unique implementation bead owner; correct its test path and Bead ID", block.ReqID, test))
		}
		if phase == "" && v.HasPhasedDelivery() {
			idx := v.FindDeliveryPhaseForFile(path)
			if idx < 0 {
				return gap(fmt.Sprintf("TEST_PLAN.md test %s has no delivery-phase owner", test))
			}
			phase = v.DeliveryPhases[idx].ID
			if !scope[strings.ToLower(phase)] {
				continue
			}
		}
		reported := strings.Contains(summary, test) || strings.Contains(summary, path) || strings.Contains(summary, testPath)
		beadExplicitlyCited := cited[owners[0].ID] && isBeadExplicitlyCitedForRepair(summary, owners[0].ID)
		named := reported || beadExplicitlyCited
		if phase == v.ActivePhaseID() || !v.HasPhasedDelivery() {
			named = named || broken[test]
		}
		anyNamed = anyNamed || named
		explicit, err := classifyTestReviewFinding(summary, test, testPath)
		if err != nil {
			return testReviewRepair{}, err
		}
		candidates = append(candidates, candidate{phase: phase, bead: owners[0], test: testPath, named: named, explicit: explicit, reported: reported, citedInSummary: beadExplicitlyCited})
		if named && phase == v.ActivePhaseID() {
			activeNamedTests[testOwner{testPath, owners[0].ID}] = true
		}
	}
	activeHasNamed := false
	for _, candidate := range candidates {
		if candidate.named && (candidate.reported || candidate.citedInSummary) && candidate.phase == v.ActivePhaseID() {
			activeHasNamed = true
			break
		}
	}
	isDeferredCompletedTest := func(candidate candidate) bool {
		return v.HasPhasedDelivery() && activeHasNamed && candidate.phase != v.ActivePhaseID() && v.IsPhaseCompleted(candidate.phase) && candidate.explicit
	}
	var selected testReviewRepair
	targetIdx := len(v.DeliveryPhases)
	for _, candidate := range candidates {
		if anyNamed && !candidate.named || !anyNamed && v.HasPhasedDelivery() && candidate.phase != v.ActivePhaseID() {
			continue
		}
		if isDeferredCompletedTest(candidate) {
			continue
		}
		if candidate.phase != v.ActivePhaseID() && activeNamedTests[testOwner{candidate.test, candidate.bead.ID}] {
			continue
		}
		idx := 0
		if v.HasPhasedDelivery() {
			for i, phase := range v.DeliveryPhases {
				if phase.ID == candidate.phase {
					idx = i
					break
				}
			}
			if idx > v.ActivePhaseIndex() {
				continue
			}
		}
		if len(selected.beads) == 0 || idx < targetIdx {
			selected = testReviewRepair{phase: candidate.phase, tests: map[string][]string{}}
			targetIdx = idx
		}
		if candidate.phase == selected.phase {
			duplicate := false
			for _, bead := range selected.beads {
				duplicate = duplicate || bead.ID == candidate.bead.ID
			}
			if !duplicate {
				selected.beads = append(selected.beads, candidate.bead)
			}
			if candidate.explicit {
				selected.tests[candidate.bead.ID] = appendUniquePath(selected.tests[candidate.bead.ID], candidate.test)
			}
		}
	}
	if len(selected.beads) == 0 {
		if fallback, ok := selectNonTestPhaseRepairBeads(v, beads, summary); ok {
			return fallback, nil
		}
		return gap("Tester rejection identifies no repairable active/completed-phase tests; correct TEST_PLAN.md phase and bead mappings")
	}
	for _, candidate := range candidates {
		if !candidate.reported || candidate.phase == selected.phase {
			continue
		}
		shared := false
		for _, chosen := range candidates {
			if chosen.phase == selected.phase && chosen.test == candidate.test && chosen.bead.ID == candidate.bead.ID && chosen.named {
				shared = true
				break
			}
		}
		if shared {
			continue
		}
		deferredPhase := candidate.phase
		if isDeferredCompletedTest(candidate) {
			deferredPhase = selected.phase
		}
		if selected.deferred == nil {
			selected.deferred = map[string]map[string][]string{}
		}
		if selected.deferred[deferredPhase] == nil {
			selected.deferred[deferredPhase] = map[string][]string{}
		}
		selected.deferred[deferredPhase][candidate.bead.ID] = appendUniquePath(selected.deferred[deferredPhase][candidate.bead.ID], candidate.test)
	}
	return selected, nil
}

// selectNonTestPhaseRepairBeads handles test_review failures in non-test-carrying
// delivery phases (e.g. static web UI or setup phases that intentionally have no
// TEST_PLAN.md rows). Instead of falsely routing to plan_gap/test_plan_rework, it
// reopens the active-phase implementation bead(s) cited in summary (or all active-phase
// implementation beads if none is named).
func selectNonTestPhaseRepairBeads(v WorkflowValidation, beads []PlanBead, summary string) (testReviewRepair, bool) {
	if !v.HasPhasedDelivery() {
		return testReviewRepair{}, false
	}
	activeID := v.ActivePhaseID()
	var activePhase *DeliveryPhase
	for i := range v.DeliveryPhases {
		if v.DeliveryPhases[i].ID == activeID {
			activePhase = &v.DeliveryPhases[i]
			break
		}
	}
	if activePhase == nil || v.PhaseShipsAutomatedTests(*activePhase) {
		return testReviewRepair{}, false
	}
	required := map[string]bool{}
	for _, f := range activePhase.RequiredFiles {
		if norm := filepath.ToSlash(strings.TrimSpace(f)); norm != "" {
			required[norm] = true
		}
	}
	lowerSummary := strings.ToLower(summary)
	var allActive, mentioned []PlanBead
	for _, b := range beads {
		if isSupersededImplementBead(b) {
			continue
		}
		rawPath, _, _ := strings.Cut(ExtractPathFromBeadTitle(b.Title, v.BeadTitleContains), " per ")
		rel := filepath.ToSlash(strings.TrimSpace(rawPath))
		if rel == "" || !required[rel] {
			continue
		}
		allActive = append(allActive, b)
		if summaryMentionsBeadID(lowerSummary, b.ID) ||
			strings.Contains(lowerSummary, strings.ToLower(rel)) ||
			strings.Contains(lowerSummary, strings.ToLower(filepath.Base(rel))) {
			mentioned = append(mentioned, b)
		}
	}
	chosen := mentioned
	if len(chosen) == 0 {
		chosen = allActive
	}
	if len(chosen) == 0 {
		return testReviewRepair{}, false
	}
	return testReviewRepair{
		phase: activeID,
		beads: chosen,
		tests: map[string][]string{},
	}, true
}

func summaryMentionsBeadID(lowerSummary, id string) bool {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return false
	}
	return strings.Contains(lowerSummary, id)
}

func reopenTestReviewRepair(townRoot, rig string, v WorkflowValidation, plan, summary string) ([]string, error) {
	ids, _, _, err := reopenTestReviewRepairWithBaseline(townRoot, rig, v, plan, summary)
	return ids, err
}

func reopenTestReviewRepairWithBaseline(townRoot, rig string, v WorkflowValidation, plan, summary string) ([]string, map[string]map[string]string, map[string]map[string][]string, error) {
	var beads []PlanBead
	statuses := map[string]string{}
	for _, status := range []string{"open", "in_progress", "closed"} {
		rows, err := listImplementBeadsForGuard(townRoot, rig, v, status)
		if err != nil {
			return nil, nil, nil, err
		}
		beads = append(beads, rows...)
		for _, bead := range rows {
			statuses[bead.ID] = status
		}
	}
	repair, err := selectTestReviewRepair(filepath.Join(townRoot, rig, "mayor", "rig"), v, plan, summary, beads)
	if err != nil {
		return nil, nil, nil, err
	}
	baseline, err := snapshotTestRepairFiles(filepath.Join(townRoot, rig, "mayor", "rig"), repair.tests)
	if err != nil {
		return nil, nil, nil, err
	}
	if repair.phase != "" && repair.phase != v.ActivePhaseID() {
		if v.RewoundFromPhaseIDField == "" {
			if err := SetRigRewoundFromPhase(townRoot, rig, v.ActivePhaseID()); err != nil {
				return nil, nil, nil, err
			}
		}
		if err := SetRigActivePhase(townRoot, rig, repair.phase); err != nil {
			return nil, nil, nil, err
		}
		beadIDs := make([]string, 0, len(repair.beads))
		for _, bead := range repair.beads {
			beadIDs = append(beadIDs, bead.ID)
		}
		logPhaseRewind(townRoot, rig, v.ActivePhaseID(), repair.phase, "test review repair", fmt.Sprintf("beads=%s deferred_phases=%d", strings.Join(beadIDs, ","), len(repair.deferred)))
	}
	var ids []string
	for _, bead := range repair.beads {
		if statuses[bead.ID] == "closed" {
			if err := bdUpdateImplementBeadStatus(townRoot, rig, bead.ID, "open"); err != nil {
				return ids, nil, nil, err
			}
			_ = resetBeadTurnCount(townRoot, rig, bead.ID)
		}
		ids = append(ids, bead.ID)
	}
	for _, bead := range beads {
		if statuses[bead.ID] == "in_progress" && bead.ID != ids[0] {
			if err := bdUpdateImplementBeadStatus(townRoot, rig, bead.ID, "open"); err != nil {
				return ids, nil, nil, err
			}
		}
	}
	if err := bdUpdateImplementBeadStatus(townRoot, rig, ids[0], "in_progress"); err != nil {
		return ids, nil, nil, err
	}
	_ = commitDoltWorkingSet(townRoot, rig)
	return ids, baseline, repair.deferred, nil
}

func testRepairFileHash(root *os.Root, path string) (string, error) {
	if !filepath.IsLocal(path) {
		return "", fmt.Errorf("invalid test repair path %q", path)
	}
	data, err := root.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", nil
	}
	if err != nil {
		return "", fmt.Errorf("read test repair file %q: %w", path, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data)), nil
}

func snapshotTestRepairFiles(rigDir string, tests map[string][]string) (map[string]map[string]string, error) {
	if len(tests) == 0 {
		return nil, nil
	}
	root, err := os.OpenRoot(rigDir)
	if err != nil {
		return nil, fmt.Errorf("open rig for test repair: %w", err)
	}
	defer root.Close()
	baseline := make(map[string]map[string]string, len(tests))
	for id, paths := range tests {
		baseline[id] = make(map[string]string, len(paths))
		for _, path := range paths {
			hash, err := testRepairFileHash(root, path)
			if err != nil {
				return nil, err
			}
			baseline[id][path] = hash
		}
	}
	return baseline, nil
}

// FilterTestRepairFilesToPhase returns only the repair files that belong to the
// currently active delivery phase. If phased delivery is not configured, all
// repair files are returned unchanged.
func FilterTestRepairFilesToPhase(v WorkflowValidation, files map[string]map[string]string) map[string]map[string]string {
	if !v.HasPhasedDelivery() || len(files) == 0 {
		return files
	}
	activeIdx := v.ActivePhaseIndex()
	filtered := make(map[string]map[string]string)
	for beadID, paths := range files {
		for path, hash := range paths {
			if v.FindDeliveryPhaseForFile(path) == activeIdx {
				if filtered[beadID] == nil {
					filtered[beadID] = make(map[string]string)
				}
				filtered[beadID][path] = hash
			}
		}
	}
	return filtered
}

// ValidateTestRepairChanges checks the original on-disk contents, not activity
// within one agent attempt. beadID limits the check to a single owner.
func ValidateTestRepairChanges(rigDir string, files map[string]map[string]string, beadID string) error {
	if len(files) == 0 {
		return nil
	}
	root, err := os.OpenRoot(rigDir)
	if err != nil {
		return fmt.Errorf("open rig for test repair: %w", err)
	}
	defer root.Close()
	for id, paths := range files {
		if beadID != "" && beadID != id {
			continue
		}
		for path, previous := range paths {
			current, err := testRepairFileHash(root, path)
			if err != nil {
				return err
			}
			if current == "missing" || current == previous {
				return fmt.Errorf("test review repair for %s is unchanged or missing: %s; address the named finding before closing or report a plan gap with evidence", id, path)
			}
		}
	}
	return nil
}

// ValidateTestRepairEvidence checks that the follow-up review cites actual lines
// in every repaired test file. It does not infer semantic coverage.
func ValidateTestRepairEvidence(rigDir string, files map[string]map[string]string) error {
	if len(files) == 0 {
		return nil
	}
	root, err := os.OpenRoot(rigDir)
	if err != nil {
		return fmt.Errorf("open rig for test review: %w", err)
	}
	defer root.Close()
	report, err := root.ReadFile(TestReportFileName)
	if err != nil {
		return fmt.Errorf("read test review evidence: %w", err)
	}
	type evidence struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Excerpt string `json:"excerpt"`
	}
	found := map[string]bool{}
	for _, line := range strings.Split(string(report), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "- Evidence: ") {
			continue
		}
		var item evidence
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "- Evidence: ")), &item); err != nil {
			return fmt.Errorf("invalid test review Evidence JSON: %w", err)
		}
		expected := false
		for _, paths := range files {
			if _, ok := paths[item.File]; ok {
				expected = true
			}
		}
		if !expected {
			continue
		}
		if !filepath.IsLocal(item.File) || item.Line < 1 || strings.TrimSpace(item.Excerpt) == "" {
			return fmt.Errorf("invalid test review evidence for %q", item.File)
		}
		data, err := root.ReadFile(item.File)
		if err != nil {
			return fmt.Errorf("read cited test file %q: %w", item.File, err)
		}
		lines := strings.Split(string(data), "\n")
		if item.Line > len(lines) || strings.TrimSpace(lines[item.Line-1]) != strings.TrimSpace(item.Excerpt) {
			return fmt.Errorf("test review evidence does not match %s line %s", item.File, strconv.Itoa(item.Line))
		}
		found[item.File] = true
	}
	for _, paths := range files {
		for path := range paths {
			if !found[path] {
				return fmt.Errorf("test review must cite a real assertion line in %s using - Evidence: {\"file\":\"...\",\"line\":1,\"excerpt\":\"...\"}", path)
			}
		}
	}
	return nil
}
