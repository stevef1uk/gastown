package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
)

type testReviewPlanGap struct{ reason string }

func (e *testReviewPlanGap) Error() string { return e.reason }

type testReviewRepair struct {
	phase string
	beads []PlanBead
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
		phase string
		bead  PlanBead
		named bool
	}
	var candidates []candidate
	anyNamed := false
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
		owners := byPath[path]
		if len(owners) == 0 {
			path = SourcePathForOwnedTest(path, phaseV)
			owners = byPath[path]
		}
		if len(owners) != 1 {
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
		named := strings.Contains(summary, test) || strings.Contains(summary, path) || cited[owners[0].ID] || broken[test]
		anyNamed = anyNamed || named
		candidates = append(candidates, candidate{phase: phase, bead: owners[0], named: named})
	}
	var selected testReviewRepair
	targetIdx := len(v.DeliveryPhases)
	for _, candidate := range candidates {
		if anyNamed && !candidate.named || !anyNamed && v.HasPhasedDelivery() && candidate.phase != v.ActivePhaseID() {
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
		}
		if len(selected.beads) == 0 || idx < targetIdx {
			selected = testReviewRepair{phase: candidate.phase}
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
		}
	}
	if len(selected.beads) == 0 {
		return gap("Tester rejection identifies no repairable active/completed-phase tests; correct TEST_PLAN.md phase and bead mappings")
	}
	return selected, nil
}

func reopenTestReviewRepair(townRoot, rig string, v WorkflowValidation, plan, summary string) ([]string, error) {
	var beads []PlanBead
	statuses := map[string]string{}
	for _, status := range []string{"open", "in_progress", "closed"} {
		rows, err := listImplementBeadsForGuard(townRoot, rig, v, status)
		if err != nil {
			return nil, err
		}
		beads = append(beads, rows...)
		for _, bead := range rows {
			statuses[bead.ID] = status
		}
	}
	repair, err := selectTestReviewRepair(filepath.Join(townRoot, rig, "mayor", "rig"), v, plan, summary, beads)
	if err != nil {
		return nil, err
	}
	if repair.phase != "" && repair.phase != v.ActivePhaseID() {
		if v.RewoundFromPhaseIDField == "" {
			if err := SetRigRewoundFromPhase(townRoot, rig, v.ActivePhaseID()); err != nil {
				return nil, err
			}
		}
		if err := SetRigActivePhase(townRoot, rig, repair.phase); err != nil {
			return nil, err
		}
	}
	var ids []string
	for _, bead := range repair.beads {
		if statuses[bead.ID] == "closed" {
			if err := bdUpdateImplementBeadStatus(townRoot, rig, bead.ID, "open"); err != nil {
				return ids, err
			}
			_ = resetBeadTurnCount(townRoot, rig, bead.ID)
		}
		ids = append(ids, bead.ID)
	}
	for _, bead := range beads {
		if statuses[bead.ID] == "in_progress" && bead.ID != ids[0] {
			if err := bdUpdateImplementBeadStatus(townRoot, rig, bead.ID, "open"); err != nil {
				return ids, err
			}
		}
	}
	if err := bdUpdateImplementBeadStatus(townRoot, rig, ids[0], "in_progress"); err != nil {
		return ids, err
	}
	_ = commitDoltWorkingSet(townRoot, rig)
	return ids, nil
}
