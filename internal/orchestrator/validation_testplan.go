package orchestrator

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// mergeTestPlanRequiredFiles folds TEST_PLAN.md "Test file:" rows into the
// delivery phase that will own them (and into the required_files union), so
// Planner/sync beads the very test files the Tester will later demand.
// Without this, the profile lists only production files and the pipeline
// deadlocks: Polecat correctly refuses unbilled files while the Tester
// correctly fails phases whose planned tests were never implemented.
//
// Routing, in order:
//  1. ### <section-id> exactly equals a phase ID (case-insensitive)
//  2. otherwise the row's DIRECTORY matches a file already required by a
//     phase (e.g. internal/api/handlers_test.go joins the phase that owns
//     internal/api/handlers.go) — Tester-authored section names frequently
//     drift from generated phase IDs, so paths are the stable signal.
//
// Rows that match neither are ignored.
func mergeTestPlanRequiredFiles(townRoot, rig string, v *WorkflowValidation) {
	if v == nil || !v.HasPhasedDelivery() {
		return
	}
	data, err := os.ReadFile(filepath.Join(townRoot, rig, "mayor", "rig", "TEST_PLAN.md"))
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return
	}

	type row struct{ id, file string }
	var rows []row
	id := ""
	seenRow := map[string]bool{}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "### ") {
			id = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "### ")))
			continue
		}
		lower := strings.ToLower(line)
		var value string
		switch {
		case strings.HasPrefix(lower, "test file:"):
			value = line[len("test file:"):]
		case strings.HasPrefix(lower, "- test file:"):
			value = line[len("- test file:"):]
		default:
			continue
		}
		f := filepath.ToSlash(strings.TrimSpace(strings.Trim(value, "`*")))
		key := strings.ToLower(f) + "|" + id
		if f == "" || seenRow[key] {
			continue
		}
		seenRow[key] = true
		rows = append(rows, row{id: id, file: f})
	}
	if len(rows) == 0 {
		return
	}

	inUnion := map[string]bool{}
	for _, f := range normalizePathList(v.RequiredFiles) {
		inUnion[strings.ToLower(f)] = true
	}

	dirOf := func(f string) string {
		s := filepath.ToSlash(strings.TrimSpace(f))
		return strings.ToLower(filepath.Dir(s))
	}

	addToPhase := func(i int, f string) {
		// Validate that test file is plausible for the project's stack.
		// For Python projects, Go test files (internal/*.go) are hallucinated.
		// For Go projects, Python test files are hallucinated. Reject them.
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".go" && WorkflowUsesPython(*v) && !WorkflowUsesGo(*v) {
			log.Printf("[test-plan-heal] phase %q: TEST_PLAN requires %s — skipped (Go file in Python project)", v.DeliveryPhases[i].ID, f)
			return
		}
		if ext == ".py" && WorkflowUsesGo(*v) && !WorkflowUsesPython(*v) {
			log.Printf("[test-plan-heal] phase %q: TEST_PLAN requires %s — skipped (Python file in Go project)", v.DeliveryPhases[i].ID, f)
			return
		}
		// Validate that test file directory matches phase's required files directories
		// e.g. defender/internal/api/handlers_test.go should not join defender/backend phase
		// and defender/test/e2e/frontend.spec.js should not join defender/tests/e2e phase (singular vs plural)
		dir := strings.ToLower(filepath.Dir(filepath.ToSlash(f)))
		if dir != "." && dir != "" {
			phaseHasDir := false
			for _, g := range normalizePathList(v.DeliveryPhases[i].RequiredFiles) {
				if strings.ToLower(filepath.Dir(filepath.ToSlash(strings.TrimSpace(g)))) == dir {
					phaseHasDir = true
					break
				}
			}
			if !phaseHasDir {
				// For test files, directory must match an existing required file's directory in that phase
				// This prevents hallucinated paths like defender/internal/api (Go) from joining Python backend,
				// and defender/test/e2e (singular) from joining defender/tests/e2e (plural)
				if IsTestImplementPath(f) {
					log.Printf("[test-plan-heal] phase %q: TEST_PLAN requires %s — skipped (test directory %q not in phase)", v.DeliveryPhases[i].ID, f, dir)
					return
				}
				// For non-test files, also check if dir is plausible
				if strings.Contains(dir, "/internal/") || strings.Contains(dir, "/api/") {
					log.Printf("[test-plan-heal] phase %q: TEST_PLAN requires %s — skipped (directory mismatch)", v.DeliveryPhases[i].ID, f)
					return
				}
			}
		}
		have := map[string]bool{}
		for _, g := range normalizePathList(v.DeliveryPhases[i].RequiredFiles) {
			have[strings.ToLower(g)] = true
		}
		key := strings.ToLower(f)
		if have[key] {
			return // already required by this phase
		}
		// Check if this file already belongs to a DIFFERENT phase
		for j, otherPhase := range v.DeliveryPhases {
			if j == i {
				continue
			}
			for _, otherFile := range otherPhase.RequiredFiles {
				if strings.EqualFold(strings.TrimSpace(otherFile), key) {
					return // file already belongs to another phase; don't reassign
				}
			}
		}
		v.DeliveryPhases[i].RequiredFiles = append(v.DeliveryPhases[i].RequiredFiles, f)
		log.Printf("[test-plan-heal] phase %q: TEST_PLAN requires %s — added to required_files",
			v.DeliveryPhases[i].ID, f)
		if !inUnion[key] {
			v.RequiredFiles = append(v.RequiredFiles, f)
			inUnion[key] = true
		}
	}

	phaseHasDir := func(i int, dir string) bool {
		for _, g := range normalizePathList(v.DeliveryPhases[i].RequiredFiles) {
			if dirOf(g) == dir {
				return true
			}
		}
		return false
	}

	for _, r := range rows {
		key := strings.ToLower(r.id)
		matched := false
		for i := range v.DeliveryPhases {
			if strings.ToLower(v.DeliveryPhases[i].ID) == key {
				addToPhase(i, r.file)
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		dir := dirOf(r.file)
		for i := range v.DeliveryPhases {
			if phaseHasDir(i, dir) {
				addToPhase(i, r.file)
				matched = true
				break
			}
		}
		_ = matched // unmatchable rows belong to another plan revision; ignore
	}
}

// TestPlanSectionIDs returns the deduplicated ### section ids declared in the
// rig's TEST_PLAN.md (lowercased, order preserved). Empty when no plan exists.
func TestPlanSectionIDs(townRoot, rig string) []string {
	data, err := os.ReadFile(filepath.Join(townRoot, rig, "mayor", "rig", "TEST_PLAN.md"))
	if err != nil {
		return nil
	}
	var ids []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if !strings.HasPrefix(line, "### ") {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "### ")))
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// MismatchedTestPlanPhaseIDs returns TEST_PLAN.md ### section ids that do NOT
// correspond to any delivery phase id in the profile. A non-empty result means
// the plan was authored against an older profile revision (e.g. before a
// spec-index --force regenerated phase IDs) and must be rewritten against the
// current IDs before it can gate any phase.
func MismatchedTestPlanPhaseIDs(townRoot, rig string, v WorkflowValidation) []string {
	plan := TestPlanSectionIDs(townRoot, rig)
	if len(plan) == 0 || len(v.DeliveryPhases) == 0 {
		return nil
	}
	valid := map[string]bool{}
	for _, p := range v.DeliveryPhases {
		valid[strings.ToLower(p.ID)] = true
	}
	var bad []string
	for _, id := range plan {
		if !valid[id] {
			bad = append(bad, id)
		}
	}
	return bad
}

// alignResult reports what alignTestPlanSectionIDs did.
type alignResult struct {
	renamed  int      // sections relabeled to current phase ids
	unalined []string // sections with no confident target (still stale)
}

// alignTestPlanSectionIDs rewrites TEST_PLAN.md ### headings that reference
// delivery phase ids which no longer exist, relabeling each section to the
// current phase inferred from where its "Test file:" rows live (directory
// affinity — the same signal mergeTestPlanRequiredFiles trusts). Section
// BODIES are preserved verbatim; only the id label changes.
//
// Returns after rewriting. A section whose rows cannot be confidently mapped
// is left untouched and reported in res.unalined (the Tester will rewrite it
// against the listed ids on the next validation pass).
func alignTestPlanSectionIDs(townRoot, rig string, v WorkflowValidation) alignResult {
	res := alignResult{}
	path := filepath.Join(townRoot, rig, "mayor", "rig", "TEST_PLAN.md")
	data, err := os.ReadFile(path)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		return res
	}

	valid := map[string]bool{}
	for _, p := range v.DeliveryPhases {
		valid[strings.ToLower(p.ID)] = true
	}
	if len(valid) == 0 {
		return res
	}
	dirToPhase := map[string]string{} // lowercase dir -> phase id (first wins)
	for _, p := range v.DeliveryPhases {
		id := strings.ToLower(p.ID)
		for _, f := range normalizePathList(p.RequiredFiles) {
			d := strings.ToLower(filepath.Dir(filepath.ToSlash(strings.TrimSpace(f))))
			if d != "." && d != "" {
				if _, ok := dirToPhase[d]; !ok {
					dirToPhase[d] = id
				}
			}
		}
	}

	lines := strings.Split(string(data), "\n")
	current := ""
	sectionDir := ""
	headingIdx := -1
	changed := false
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "### ") {
			headingIdx = i
			current = strings.TrimSpace(strings.TrimPrefix(line, "### "))
			lower := strings.ToLower(current)
			if !valid[lower] {
				sectionDir = "" // reset until rows establish affinity
			} else {
				sectionDir = "@" // already canonical; lock from remap
			}
			continue
		}
		lower := strings.ToLower(line)
		var value string
		switch {
		case strings.HasPrefix(lower, "test file:"):
			value = line[len("test file:"):]
		case strings.HasPrefix(lower, "- test file:"):
			value = line[len("- test file:"):]
		default:
			continue
		}
		f := filepath.ToSlash(strings.TrimSpace(strings.Trim(value, "`*")))
		if f == "" || sectionDir == "@" {
			continue
		}
		d := strings.ToLower(filepath.Dir(f))
		if d == "." || d == "" {
			continue
		}
		if sectionDir == "" && headingIdx >= 0 {
			if target, ok := dirToPhase[d]; ok {
				lines[headingIdx] = "### " + target
				res.renamed++

				changed = true
				log.Printf("[test-plan-align] relabeled section %q -> %q (rows live under %s)",
					current, target, d)
				current = target
				sectionDir = "@"
			} else if sectionDir == "" {
				sectionDir = "?"
			}
			continue
		}
	}
	_ = sectionDir

	if os.Getenv("TESTPLAN_ALIGN_DEBUG") != "" {
		fmt.Println("---- align result ----")
		for li, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "###") || li < 4 {
				fmt.Printf("%03d|%s\n", li, l)
			}
		}
	}
	if changed {
		if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			log.Printf("[test-plan-align] rewrite %s failed: %v", path, err)
			return res
		}
		log.Printf("[test-plan-align] TEST_PLAN.md aligned to current delivery phases (%d section(s) relabeled)", res.renamed)
	}
	return res
}

// alignArchitectureWithTestPlan appends TEST_PLAN.md "Test file:" paths that
// are missing from architecture.md's file layout section. Without this, the
// triad check (SPEC ↔ Architecture ↔ Plan) flags every test file the Tester
// plans because the Architect — who writes architecture.md BEFORE the Tester
// writes TEST_PLAN.md — cannot know about test files that don't exist yet.
//
// This runs automatically whenever the profile is clamped for a specific rig,
// keeping all three documents in sync without manual intervention.
func alignArchitectureWithTestPlan(townRoot, rig string) {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	archPath := filepath.Join(rigDir, "architecture.md")
	archData, err := os.ReadFile(archPath)
	if err != nil || len(archData) == 0 {
		return
	}
	planPath := filepath.Join(rigDir, "TEST_PLAN.md")
	planData, err := os.ReadFile(planPath)
	if err != nil || len(planData) == 0 {
		return
	}

	existing := string(archData)
	var missing []string
	for _, raw := range strings.Split(string(planData), "\n") {
		line := strings.TrimSpace(raw)
		lower := strings.ToLower(line)
		if !strings.HasPrefix(lower, "test file:") && !strings.HasPrefix(lower, "- test file:") {
			continue
		}
		f := filepath.ToSlash(strings.TrimSpace(strings.Trim(line[len(line)-len(lower)+len("test file:"):], "`*")))
		if f == "" {
			continue
		}
		// Check if this path appears in architecture.md
		if strings.Contains(existing, "`"+f+"`") {
			continue
		}
		missing = append(missing, f)
	}
	if len(missing) == 0 {
		return
	}

	// Append under the last file-list entry in architecture.md
	var out []string
	inserted := false
	for _, line := range strings.Split(existing, "\n") {
		out = append(out, line)
		if inserted {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- `linkshelf/") && strings.HasSuffix(trimmed, "`") {
			// This is the last file in the layout list; insert missing entries after it
			for _, m := range missing {
				out = append(out, "- `"+m+"`")
			}
			inserted = true
		}
	}
	if !inserted {
		return // couldn't find file-list section to append to
	}

	if err := os.WriteFile(archPath, []byte(strings.Join(out, "\n")), 0o644); err != nil {
		log.Printf("[test-plan-align] failed to update %s: %v", archPath, err)
		return
	}
	log.Printf("[test-plan-align] added %d TEST_PLAN test file(s) to architecture.md: %v", len(missing), missing)
}
