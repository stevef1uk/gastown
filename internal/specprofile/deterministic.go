package specprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/orchestrator"
)

var (
	specServerPortFlagRE  = regexp.MustCompile(`(?i)(?:--port|--port=|-p\s+)\s*:?(\d{2,5})`)
	specLocalhostPortRE   = regexp.MustCompile(`(?i)(?:localhost|127\.0\.0\.1):(\d{2,5})`)
	specUvicornRE         = regexp.MustCompile(`(?i)\buvicorn\s+\S+:\S+`)
	specFlaskRunRE        = regexp.MustCompile(`(?i)\bflask\s+run\b`)
	specGunicornRE        = regexp.MustCompile(`(?i)\bgunicorn\b`)
	specHypercornRE       = regexp.MustCompile(`(?i)\bhypercorn\b`)
	specGoRunRE           = regexp.MustCompile(`(?i)\bgo\s+run\b`)
	specNodeListenRE      = regexp.MustCompile(`(?i)\bnode\s+\S+.*listen`)
	specHTTProbeRE        = regexp.MustCompile(`(?i)\b(curl|wget|http://|https://)`)
	specHTTPTableRE       = regexp.MustCompile(`(?im)^\|\s*(GET|POST|PUT|DELETE|PATCH)\s*\|`)
)

func inferDevServerPort(spec string, paths []string) int {
	lower := strings.ToLower(spec)

	hasServerCmd := specUvicornRE.MatchString(spec) ||
		specFlaskRunRE.MatchString(spec) ||
		specGunicornRE.MatchString(spec) ||
		specHypercornRE.MatchString(spec) ||
		specGoRunRE.MatchString(spec) ||
		(strings.Contains(lower, "node ") && specNodeListenRE.MatchString(spec))

	hasCurl := strings.Contains(lower, "curl") && (strings.Contains(lower, "http://") || strings.Contains(lower, "https://") || strings.Contains(lower, "localhost:"))
	hasHTTPTable := specHTTPTableRE.MatchString(spec)

	if !hasServerCmd && !hasCurl && !hasHTTPTable {
		return 0
	}

	if m := specServerPortFlagRE.FindStringSubmatch(spec); len(m) >= 2 {
		if port, err := strconv.Atoi(m[1]); err == nil && port > 0 && port < 65536 {
			return port
		}
	}
	if m := specLocalhostPortRE.FindStringSubmatch(spec); len(m) >= 2 {
		if port, err := strconv.Atoi(m[1]); err == nil && port > 0 && port < 65536 {
			return port
		}
	}

	// Infer default port from the server command type in the SPEC
	if specUvicornRE.MatchString(spec) || specFlaskRunRE.MatchString(spec) || specGunicornRE.MatchString(spec) || specHypercornRE.MatchString(spec) {
		return 8000
	}
	if specGoRunRE.MatchString(spec) {
		return 8080
	}
	if strings.Contains(lower, "node ") && specNodeListenRE.MatchString(spec) {
		return 3000
	}
	// Fallback to test runner heuristic
	switch inferTestRunner(paths) {
	case "pytest":
		return 8000
	case "npm":
		return 3000
	case "go":
		return 8080
	}
	return 8080
}

// DeterministicIndexRig creates a workflow profile from SPEC.md WITHOUT LLM hallucinations.
// Uses SPEC layout tree + phases section for required_files. Calls LLM to assign files to
// SPEC-named phases, then validates deterministically: union of all phase files MUST exactly
// equal the parser's file set (no missing, no extras/hallucinations). Falls back to
// directory-based grouping if LLM fails validation.
func DeterministicIndexRig(ctx context.Context, townRoot, rig string) (*ProfileFile, error) {
	specPath := SpecPath(townRoot, rig)
	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", specPath, err)
	}
	spec := string(data)

	// Parse SPEC layout tree for required_files
	mayorRig := filepath.Join(townRoot, rig, "mayor", "rig")
	paths, hasTree := orchestrator.ProbeExtractSpecLayoutPaths(mayorRig)
	log.Printf("[deterministic-index] rig=%s hasTree=%v paths=%d: %v", rig, hasTree, len(paths), paths)
	if !hasTree || len(paths) == 0 {
		log.Printf("[deterministic-index] no parseable layout tree in SPEC for %s — falling back to LLM", rig)
		return nil, fmt.Errorf("no parseable layout tree in SPEC — falling back to LLM")
	}

	// Parse phases from SPEC (names only, no file assignments)
	phases := parseSpecPhases(spec)
	log.Printf("[deterministic-index] rig=%s phases from SPEC=%d: %v", rig, len(phases), phaseNames(phases))
	if len(phases) == 0 {
		// Fallback: default phases from tree structure
		phases = defaultPhasesFromPaths(paths)
	} else if hasEmptyPhaseFiles(phases) {
		// SPEC has phase names but no file lists — use LLM to assign files to phases
		assigned, ok := assignFilesToPhasesViaLLM(ctx, townRoot, rig, spec, phases, paths)
		if ok {
			phases = assigned
		} else {
			// LLM validation failed — fall back to deterministic grouping
			log.Printf("[deterministic-index] LLM phase assignment failed validation; falling back to directory-based grouping for %s", rig)
			phases = defaultPhasesFromPaths(paths)
		}
	}

	// Parse verify commands from SPEC
	verifyCmd := inferVerifyCommand(spec, paths)

	v := orchestrator.WorkflowValidation{
		LayoutRoot:         inferLayoutRoot(paths),
		BeadTitleContains:  "Implement " + inferLayoutRoot(paths) + "/",
		RequiredFiles:      paths,
		QAVerifyCommand:    verifyCmd,
		TestRunner:         inferTestRunner(paths),
		DeliveryPhases:     phases,
		ActivePhaseIDField: phases[0].ID,
		DevServerPort:      inferDevServerPort(spec, paths),
	}

	// Clamp/validate
	v = orchestrator.ClampProfileValidation(v)
	log.Printf("[deterministic-index] after ClampProfileValidation: integration-test required_files=%v", v.DeliveryPhases[len(v.DeliveryPhases)-1].RequiredFiles)
	v = orchestrator.SanitizeRigFlowProfile(v)
	log.Printf("[deterministic-index] after SanitizeRigFlowProfile: integration-test required_files=%v", v.DeliveryPhases[len(v.DeliveryPhases)-1].RequiredFiles)

	f := ProfileFile{
		Version:     1,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Source:      "deterministic",
		Confidence:  "high",
		Validation:  v,
	}

	if err := orchestrator.WriteRigWorkflowProfile(townRoot, rig, f.Validation, f.Source, f.Confidence); err != nil {
		return nil, err
	}

	// JUDGE pipeline: enhance verify commands using clamped profile + SPEC/REQUIREMENTS
	reqPath := RequirementsPath(townRoot, rig)
	reqRaw, _ := os.ReadFile(reqPath)
	reqText := string(reqRaw)
	specText := spec
	if specText != "" || reqText != "" {
		log.Printf("[deterministic-index] judge: loaded %d chars spec + %d chars req", len(specText), len(reqText))
	}

	endpoint, model := ResolveLLMForSpecIndex(townRoot)
	validatorEndpoint, validatorModel := ResolveValidatorLLMForSpecIndex(townRoot)
	f.Validation = JudgePhaseVerifyCommands(ctx, endpoint, model, validatorEndpoint, validatorModel, f.Validation, specText, reqText)

	// Deterministic guard against judge hallucinations: never leave go test/vet/run
	// in a Python phase, or curl smoke when dev_server_port=0.
	f.Validation = orchestrator.SanitizePhaseVerifyCommandsForStack(f.Validation)

	// Write again WITHOUT re-clamping to preserve JUDGE enhancements
	if err := orchestrator.WriteRigWorkflowProfileClamped(townRoot, rig, f.Validation, f.Source, f.Confidence, false); err != nil {
		return nil, err
	}

	// Re-read final on-disk version
	path := ProfilePath(townRoot, rig)
	raw, err := os.ReadFile(path)
	if err == nil {
		_ = json.Unmarshal(raw, &f)
	}

	log.Printf("[deterministic-index] wrote profile for %s: %d files, %d phases", rig, len(paths), len(phases))
	return &f, nil
}

func phaseNames(phases []orchestrator.DeliveryPhase) []string {
	names := make([]string, len(phases))
	for i, p := range phases {
		names[i] = p.ID
	}
	return names
}

func hasEmptyPhaseFiles(phases []orchestrator.DeliveryPhase) bool {
	for _, p := range phases {
		if len(p.RequiredFiles) == 0 {
			return true
		}
	}
	return false
}

// assignFilesToPhasesViaLLM asks the LLM to map parser-extracted files to SPEC-named phases.
// Input: whole SPEC + phase names + exact file list. Output: phaseID -> files.
// Validation (deterministic): union of all phase files MUST exactly equal the input file set.
// Returns the assigned phases and true if validation passes; false if LLM hallucinated/omitted files.
func assignFilesToPhasesViaLLM(ctx context.Context, townRoot, rig, spec string, phases []orchestrator.DeliveryPhase, files []string) ([]orchestrator.DeliveryPhase, bool) {
	log.Printf("[deterministic-index] assignFilesToPhasesViaLLM called for %s: %d phases, %d files", rig, len(phases), len(files))
	if len(phases) == 0 || len(files) == 0 {
		return phases, false
	}

	// FIRST: Try deterministic assignment based on phase titles/keywords matching file paths.
	// This avoids LLM hallucination when SPEC has clear phase-to-file mapping.
	if assigned := deterministicAssignFilesToPhases(phases, files); assigned != nil {
		log.Printf("[deterministic-index] deterministic assignment succeeded for %s", rig)
		return assigned, true
	}

	// FALLBACK: LLM assignment (can hallucinate) - with retry until proxy is available
	endpoint, model := ResolveLLMForSpecIndex(townRoot)

	// Build phase ID list for the LLM
	phaseIDs := make([]string, len(phases))
	for i, p := range phases {
		phaseIDs[i] = p.ID
	}

	// Extract relevant SPEC excerpts per phase using RAG (keyword-scored chunk matching)
	// This avoids sending the entire SPEC.md to the LLM which could exceed token limits.
	specExcerpts := extractPhaseSpecExcerpts(phases, spec)

	system := `You assign files to delivery phases. Input: a SPEC, a list of phase IDs, and an EXACT list of ALL file paths that MUST be covered.
Output a single JSON object mapping phase_id -> list of file paths from the input file list ONLY.
Rules:
- Every file from the input file list MUST appear exactly once across all phases.
- Do NOT add files not in the input list. Do NOT omit files.
- Only output the JSON mapping. No prose.`

	phaseList := strings.Join(phaseIDs, ", ")

	// Build SPEC excerpts for prompt (only relevant sections per phase)
	var specExcerptBuilder strings.Builder
	for _, p := range phases {
		if excerpt, ok := specExcerpts[p.ID]; ok && excerpt != "" {
			specExcerptBuilder.WriteString(fmt.Sprintf("=== Phase: %s (%s) ===\n%s\n\n", p.ID, p.Title, excerpt))
		}
	}
	specRelevant := specExcerptBuilder.String()

	// Include the SPEC title + Overview as system summary so the LLM
	// understands what the project is before assigning files to phases.
	systemSummary := extractSpecOverview(spec)

	user := fmt.Sprintf(`SYSTEM SUMMARY:
%s

RELEVANT SPEC EXCERPTS (per phase):
%s

PHASES: %s
FILES (must all be assigned exactly once):
%s

Return JSON: { "phase-id": ["file1", "file2"], ... }`, systemSummary, specRelevant, phaseList, strings.Join(files, "\n"))

	// Retry LLM assignment with exponential backoff until proxy is available or context cancelled
	var content string
	var err error
	maxRetries := 30
	baseDelay := 2 * time.Second
	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, false
		default:
		}

		content, err = chatCompletionJSON(ctx, endpoint, model, system, user)
		if err == nil {
			break
		}

		// Check if it's a connection error (proxy not running)
		if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "dial tcp") {
			delay := baseDelay * time.Duration(1<<(attempt-1))
			if delay > 60*time.Second {
				delay = 60 * time.Second
			}
			log.Printf("[deterministic-index] LLM proxy not available (attempt %d/%d), retrying in %v: %v", attempt, maxRetries, delay, err)
			select {
			case <-ctx.Done():
				return nil, false
			case <-time.After(delay):
			}
			continue
		}

		// Other errors - log and return failure
		log.Printf("[deterministic-index] LLM phase assignment call failed: %v", err)
		return nil, false
	}

	if err != nil {
		log.Printf("[deterministic-index] LLM phase assignment failed after %d attempts: %v", maxRetries, err)
		return nil, false
	}

	var phaseFiles map[string][]string
	// Use the robust extractor: it finds the first balanced JSON object even when
	// the LLM wraps it in markdown fences, reasoning tags, or prose.
	if err := ExtractJSONObject(content, &phaseFiles); err != nil {
		log.Printf("[deterministic-index] LLM phase assignment JSON parse failed: %v (raw: %.200s)", err, content)
		return phases, false
	}
	log.Printf("[deterministic-index] LLM returned phaseFiles keys: %v", func() []string { k := make([]string, 0, len(phaseFiles)); for kk := range phaseFiles { k = append(k, kk) }; return k }())
	for pid, fs := range phaseFiles {
		log.Printf("[deterministic-index]   %s: %v", pid, fs)
	}

	// Deterministic validation: every input file appears exactly once
	fileSet := make(map[string]bool, len(files))
	for _, f := range files {
		fileSet[f] = true
	}
	seen := make(map[string]int)
	for phaseID, fs := range phaseFiles {
		for _, f := range fs {
			if !fileSet[f] {
				log.Printf("[deterministic-index] LLM assigned file not in parser set: %s (phase %s)", f, phaseID)
				return phases, false
			}
			seen[f]++
		}
	}
	// Check all files covered exactly once
	for f, count := range seen {
		if count != 1 {
			log.Printf("[deterministic-index] LLM file coverage error: %s seen %d times", f, count)
			return phases, false
		}
	}
	for f := range fileSet {
		if seen[f] == 0 {
			log.Printf("[deterministic-index] LLM omitted file from phases: %s", f)
			return phases, false
		}
	}

	// Post-fix: ensure integration-test phase has the server entry point
	// (integration tests need the server binary to run). Also ensure test files
	// stay in their respective phases (not in integration-test).
	if itFiles, ok := phaseFiles["integration-test"]; ok {
		log.Printf("[deterministic-index] integration-test phase files before fix: %v", itFiles)

		// Find the main entry point based on test runner
		testRunner := inferTestRunner(files)
		entryPatterns := getEntryPointPatterns(testRunner)
		layoutRoot := inferLayoutRoot(files)
		var entryFile string
		for _, f := range files {
			for _, pattern := range entryPatterns {
				// Match with or without layout_root prefix
				if strings.HasSuffix(f, pattern) || strings.HasSuffix(f, layoutRoot+"/"+pattern) {
					entryFile = f
					break
				}
			}
			if entryFile != "" {
				break
			}
		}

		// Build final file list: existing non-test files + entry point
		nonTestFiles := make([]string, 0)
		for _, f := range itFiles {
			if !strings.HasSuffix(f, "_test.go") && !strings.HasSuffix(f, "_test.py") {
				nonTestFiles = append(nonTestFiles, f)
			}
		}
		if entryFile != "" {
			// Check if entry already in nonTestFiles
			found := false
			for _, f := range nonTestFiles {
				if f == entryFile {
					found = true
					break
				}
			}
			if !found {
				nonTestFiles = append(nonTestFiles, entryFile)
			}
		}
		phaseFiles["integration-test"] = nonTestFiles
		log.Printf("[deterministic-index] assigned %s to integration-test phase (testRunner=%s)", entryFile, testRunner)
		log.Printf("[deterministic-index] integration-test phase files after fix: %v", phaseFiles["integration-test"])
	} else {
		log.Printf("[deterministic-index] integration-test phase NOT FOUND in phaseFiles map; keys: %v", func() []string { k := make([]string, 0, len(phaseFiles)); for kk := range phaseFiles { k = append(k, kk) }; return k }())
	}

	// Build phases with assigned files (preserving SPEC phase order)
	assigned := make([]orchestrator.DeliveryPhase, len(phases))
	for i, p := range phases {
		fs := phaseFiles[p.ID]
		if fs == nil {
			fs = []string{}
		}
		assigned[i] = orchestrator.DeliveryPhase{
			ID:              p.ID,
			Title:           p.Title,
			RequiredFiles:   fs,
			QAVerifyCommand: "",
			SpecFocus:       p.SpecFocus,
		}
	}

	// Final deterministic check on the ASSIGNED phases: if the LLM returned files
	// under unknown phase IDs, the lookup above silently drops them. Re-verify
	// that the phases we're about to return cover every input file exactly once.
	assignedCount := make(map[string]int)
	for _, p := range assigned {
		for _, f := range p.RequiredFiles {
			assignedCount[f]++
		}
	}
	for f, count := range assignedCount {
		if count != 1 {
			log.Printf("[deterministic-index] assigned-phase coverage error: %s seen %d times", f, count)
			return phases, false
		}
	}
	for f := range fileSet {
		if assignedCount[f] == 0 {
			log.Printf("[deterministic-index] assigned-phase omitted file: %s", f)
			return phases, false
		}
	}

	return assigned, true
}

// deterministicAssignFilesToPhases assigns files to phases based on keyword matching
// between phase titles/spec_focus and file paths. Returns nil if unable to assign all files.
func deterministicAssignFilesToPhases(phases []orchestrator.DeliveryPhase, files []string) []orchestrator.DeliveryPhase {
	// Build keyword map for each phase from title and spec_focus
	type phaseKeywords struct {
		phase  *orchestrator.DeliveryPhase
		keys   []string
	}
	var pk []phaseKeywords
	for i := range phases {
		p := &phases[i]
		keys := []string{}
		// Extract keywords from title
		for _, w := range strings.Fields(strings.ToLower(p.Title)) {
			if len(w) > 3 {
				keys = append(keys, strings.Trim(w, "`\"',.:;()[]{}"))
			}
		}
		// Extract keywords from spec_focus
		for _, w := range strings.Fields(strings.ToLower(p.SpecFocus)) {
			if len(w) > 3 {
				keys = append(keys, strings.Trim(w, "`\"',.:;()[]{}"))
			}
		}
		// Deduplicate
		seen := map[string]bool{}
		var uniq []string
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				uniq = append(uniq, k)
			}
		}
		pk = append(pk, phaseKeywords{phase: p, keys: uniq})
	}

	// Match files to phases
	fileToPhase := map[string]*orchestrator.DeliveryPhase{}
	unmatched := []string{}

	for _, f := range files {
		fLower := strings.ToLower(f)
		var bestMatch *orchestrator.DeliveryPhase
		bestScore := 0

		for i := range pk {
			score := 0
			for _, key := range pk[i].keys {
				if strings.Contains(fLower, key) {
					score += len(key) // longer keywords score higher
				}
			}
			if score > bestScore {
				bestScore = score
				bestMatch = pk[i].phase
			}
		}

		if bestMatch != nil && bestScore > 0 {
			fileToPhase[f] = bestMatch
		} else {
			unmatched = append(unmatched, f)
		}
	}

	// If too many unmatched, fail
	if len(unmatched) > len(files)/2 {
		log.Printf("[deterministic-index] deterministic assignment failed: %d unmatched files: %v", len(unmatched), unmatched)
		return nil
	}

	// Assign unmatched to closest phase by filename similarity
	for _, f := range unmatched {
		fBase := strings.ToLower(filepath.Base(f))
		var bestMatch *orchestrator.DeliveryPhase
		bestScore := 0
		for i := range pk {
			score := 0
			for _, key := range pk[i].keys {
				if strings.Contains(fBase, key) {
					score += len(key)
				}
			}
			if score > bestScore {
				bestScore = score
				bestMatch = pk[i].phase
			}
		}
		if bestMatch != nil {
			fileToPhase[f] = bestMatch
		} else if len(phases) > 0 {
			// Last resort: first phase
			fileToPhase[f] = &phases[0]
		}
	}

	// Build phase file assignments
	phaseFiles := map[string][]string{}
	for _, p := range phases {
		phaseFiles[p.ID] = []string{}
	}
	for f, p := range fileToPhase {
		phaseFiles[p.ID] = append(phaseFiles[p.ID], f)
	}

	// Build assigned phases
	assigned := make([]orchestrator.DeliveryPhase, len(phases))
	for i, p := range phases {
		fs := phaseFiles[p.ID]
		if fs == nil {
			fs = []string{}
		}
		assigned[i] = orchestrator.DeliveryPhase{
			ID:              p.ID,
			Title:           p.Title,
			RequiredFiles:   fs,
			QAVerifyCommand: "",
			SpecFocus:       p.SpecFocus,
		}
	}

	// Verify all files assigned exactly once
	allAssigned := map[string]int{}
	for _, fs := range phaseFiles {
		for _, f := range fs {
			allAssigned[f]++
		}
	}
	for f, count := range allAssigned {
		if count != 1 {
			log.Printf("[deterministic-index] deterministic assignment duplicate/omitted: %s count=%d", f, count)
			return nil
		}
	}
	for _, f := range files {
		if allAssigned[f] == 0 {
			log.Printf("[deterministic-index] deterministic assignment omitted: %s", f)
			return nil
		}
	}

	log.Printf("[deterministic-index] deterministic assignment succeeded: %d phases, %d files", len(phases), len(files))
	return assigned
}

// extractSpecOverview returns the SPEC title line plus the ## Overview section
// (a compact system summary for the LLM). Falls back to the first 10 lines if
// no explicit Overview heading is present.
// Summary-section keyword matcher. Recognizes headings like:
//   ## Overview            ## System Overview
//   ## Summary             ## System Summary
//   ## Vision              ## 1. Vision
//   ## Introduction        ## Project Specification (wrapper, usually empty)
func specOverviewHeadingRE() *regexp.Regexp {
	return regexp.MustCompile(`(?i)\b(?:overview|summary|vision|introduction|background|purpose|goal|about|description|specification)\b`)
}

var specHeadingLevelRE = regexp.MustCompile(`^(#+)`)

// headingLevel returns the markdown heading level (1..6) or 0 if not a heading.
func headingLevel(line string) int {
	m := specHeadingLevelRE.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return 0
	}
	level := len(m[1])
	rest := strings.TrimSpace(line[level:])
	if rest == "" {
		return 0
	}
	return level
}

// isSummaryHeading reports whether a line is a heading that names the project
// summary section, e.g. "## Overview", "## 1. Vision", "## System Summary".
// Leading numbering/punctuation is stripped before keyword matching.
func isSummaryHeading(line string) bool {
	level := headingLevel(line)
	if level < 2 {
		return false
	}
	h := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
	h = regexp.MustCompile(`^\d+[\.\)\-]?\s*`).ReplaceAllString(h, "")
	return specOverviewHeadingRE().MatchString(h)
}

// extractSpecOverview returns the SPEC title plus the project summary section
// (Overview / Summary / Vision / etc.), which the LLM uses as system context
// before assigning files to phases. Wrapper headings with no content (e.g.
// "## Project Specification") are skipped in favor of the real summary that
// follows. Falls back to the first 10 non-empty lines if no summary heading
// with content is found.
func extractSpecOverview(spec string) string {
	lines := strings.Split(spec, "\n")

	var title []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if headingLevel(trimmed) == 1 {
			title = append(title, line)
		}
	}

	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if headingLevel(trimmed) < 2 || !isSummaryHeading(trimmed) {
			continue
		}
		// Collect content until the next heading of level 1 or 2.
		var content []string
		j := i + 1
		for j < len(lines) {
			nextTrim := strings.TrimSpace(lines[j])
			if lvl := headingLevel(nextTrim); lvl == 1 || lvl == 2 {
				break
			}
			if nextTrim != "" {
				content = append(content, lines[j])
			}
			j++
		}
		if len(content) > 0 {
			return strings.TrimSpace(strings.Join(append(title, content...), "\n"))
		}
		// Wrapper heading with no content — keep scanning for the real summary.
		i = j - 1
	}

	// Fallback: first 10 non-empty lines
	var fallback []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fallback = append(fallback, line)
		if len(fallback) >= 10 {
			break
		}
	}
	return strings.TrimSpace(strings.Join(fallback, "\n"))
}

func parseSpecPhases(spec string) []orchestrator.DeliveryPhase {
	// Extract "## Phases" or "## Delivery Phases" section
	lower := strings.ToLower(spec)
	for _, marker := range []string{"## phases", "## delivery phases"} {
		i := strings.Index(lower, marker)
		if i < 0 {
			continue
		}
		section := spec[i:]
		if j := strings.Index(section[1:], "\n## "); j >= 0 {
			section = section[:1+j]
		}
		// Try table format first (| Phase | Description | Success Criteria |)
		phases := parsePhaseTable(section)
		if len(phases) > 0 {
			return phases
		}
		// Fallback to list format
		return parsePhaseList(section)
	}
	return nil
}

func parsePhaseTable(section string) []orchestrator.DeliveryPhase {
	lines := strings.Split(section, "\n")
	var phases []orchestrator.DeliveryPhase
	inTable := false
	foundHeader := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Look for the phase table header specifically: | Phase | Description | Success Criteria |
		if strings.HasPrefix(trimmed, "|") && strings.Contains(trimmed, "Phase") && strings.Contains(trimmed, "Description") && strings.Contains(trimmed, "Success") {
			foundHeader = true
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if strings.HasPrefix(trimmed, "|") && strings.Contains(trimmed, "---") {
			continue // separator row
		}
		if strings.HasPrefix(trimmed, "|") {
			// Parse table row: | 1 | Scaffold project... | ...
			parts := strings.Split(trimmed, "|")
			if len(parts) >= 3 {
				phaseNum := strings.TrimSpace(parts[1])
				desc := strings.TrimSpace(parts[2])
				if phaseNum != "" && desc != "" {
					// Create phase ID from description
					id := slugify(desc)
					if id == "" {
						id = "phase-" + phaseNum
					}
					phases = append(phases, orchestrator.DeliveryPhase{
						ID:              id,
						Title:           desc,
						RequiredFiles:   []string{},
						QAVerifyCommand: "",
						SpecFocus:       desc,
					})
				}
			}
		} else if trimmed != "" && !strings.HasPrefix(trimmed, "#") && foundHeader {
			// End of table after we found the header
			break
		}
	}
	return phases
}

// leadingIndent returns the number of leading spaces/tabs in a line
func leadingIndent(line string) int {
	count := 0
	for _, r := range line {
		if r == ' ' || r == '\t' {
			count++
		} else {
			break
		}
	}
	return count
}

// parsePhaseList extracts top-level phase items from the Phases section.
// Only lines with indentation == 0 are considered (sub-items are ignored).
// Leading numbers, bullets, and special chars are stripped from phase names.
// Also captures the phase description from indented sub-items for spec_focus.
func parsePhaseList(section string) []orchestrator.DeliveryPhase {
	lines := strings.Split(section, "\n")
	var phases []orchestrator.DeliveryPhase
	phaseNum := 0

	var currentPhase *orchestrator.DeliveryPhase
	var currentPhaseLines []string

	for _, line := range lines {
		// Check indentation - only process top-level lines (indent == 0)
		indent := leadingIndent(line)
		
		if indent == 0 {
			// This is a top-level line - check if it's a phase marker
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "```") {
				continue
			}

			// If we have a current phase, save it
			if currentPhase != nil {
				// Build spec_focus from accumulated description lines
				if len(currentPhaseLines) > 0 {
					currentPhase.SpecFocus = currentPhase.Title + "\n\n" + strings.Join(currentPhaseLines, "\n")
				}
				phases = append(phases, *currentPhase)
			}

			// Try to parse this line as a new phase
			currentPhase = parsePhaseLine(trimmed)
			currentPhaseLines = nil
			if currentPhase != nil {
				phaseNum++
			}
		} else if currentPhase != nil && indent > 0 {
			// This is an indented sub-item - add to current phase's description
			trimmed := strings.TrimSpace(line)
			if trimmed != "" && !strings.HasPrefix(trimmed, "```") {
				currentPhaseLines = append(currentPhaseLines, trimmed)
			}
		}
	}

	// Don't forget the last phase
	if currentPhase != nil {
		if len(currentPhaseLines) > 0 {
			currentPhase.SpecFocus = currentPhase.Title + "\n\n" + strings.Join(currentPhaseLines, "\n")
		}
		phases = append(phases, *currentPhase)
	}

	return phases
}

// parsePhaseLine parses a single top-level line and returns a phase if it matches a known format
func parsePhaseLine(trimmed string) *orchestrator.DeliveryPhase {
	if trimmed == "" || strings.HasPrefix(trimmed, "```") {
		return nil
	}

	var name string
	var id string
	matched := false

	// Format 1: Numbered with bold: "1. **Phase Name**"
	if len(trimmed) > 2 && trimmed[0] >= '1' && trimmed[0] <= '9' && trimmed[1] == '.' {
		rest := trimmed[strings.Index(trimmed, ".")+1:]
		rest = strings.TrimSpace(rest)
		if start := strings.Index(rest, "**"); start >= 0 && start < 5 {
			if end := strings.Index(rest[start+2:], "**"); end >= 0 {
				name = rest[start+2 : start+2+end]
				matched = true
			}
		}
		// Format 1b: Numbered without bold: "1. Phase Name"
		if !matched && strings.Contains(rest, " ") {
			name = strings.TrimSpace(rest)
			matched = true
		}
	}

	// Format 2: Bulleted with bold: "- **Phase Name**"
	if !matched && strings.HasPrefix(trimmed, "- **") {
		rest := trimmed[4:] // "- **" = 4 chars
		if end := strings.Index(rest, "**"); end >= 0 {
			name = rest[:end]
			matched = true
		}
	}

	// Format 3: Bulleted without bold: "- Phase Name"
	if !matched && strings.HasPrefix(trimmed, "- ") {
		rest := trimmed[2:]
		if rest != "" {
			name = strings.TrimSpace(rest)
			matched = true
		}
	}

	// Format 4: "Phase 1 — Project Foundation" or "Phase 1 - Name" or "Phase 1: Name"
	if !matched && strings.HasPrefix(trimmed, "Phase ") {
		rest := strings.TrimPrefix(trimmed, "Phase ")
		if idx := strings.Index(rest, " "); idx > 0 {
			rest = rest[idx+1:]
			rest = strings.TrimLeft(rest, " \t—-:") // Trim leading punctuation/whitespace
			for _, delim := range []string{" — ", " - ", ": "} {
				if strings.Contains(rest, delim) {
					parts := strings.SplitN(rest, delim, 2)
					if len(parts) == 2 && parts[1] != "" {
						name = strings.TrimSpace(parts[1])
						matched = true
						break
					}
				}
			}
			if !matched && strings.TrimSpace(rest) != "" {
				name = strings.TrimSpace(rest)
				matched = true
			}
		}
	}

	// Format 5: Markdown header "# Phase 1 — Project Foundation" or "## Phase 1 — Name"
	if !matched && strings.HasPrefix(trimmed, "#") {
		rest := strings.TrimLeft(trimmed, "#")
		rest = strings.TrimSpace(rest)
		if strings.HasPrefix(rest, "Phase ") {
			rest = strings.TrimPrefix(rest, "Phase ")
			if idx := strings.Index(rest, " "); idx > 0 {
				rest = rest[idx+1:]
				rest = strings.TrimLeft(rest, " \t—-:") // Trim leading punctuation/whitespace
				for _, delim := range []string{" — ", " - ", ": "} {
					if strings.Contains(rest, delim) {
						parts := strings.SplitN(rest, delim, 2)
						if len(parts) == 2 && parts[1] != "" {
							name = strings.TrimSpace(parts[1])
							matched = true
							break
						}
					}
				}
				if !matched && strings.TrimSpace(rest) != "" {
					name = strings.TrimSpace(rest)
					matched = true
				}
			}
		}
	}

	// Format 6: Dash-separated: "go-module - Initialize go.mod" or "go-module: Initialize go.mod"
	if !matched && (strings.Contains(trimmed, " - ") || strings.Contains(trimmed, ": ")) {
		parts := strings.SplitN(trimmed, " - ", 2)
		if len(parts) != 2 {
			parts = strings.SplitN(trimmed, ": ", 2)
		}
		if len(parts) == 2 {
			idPart := strings.TrimSpace(parts[0])
			descPart := strings.TrimSpace(parts[1])
			if idPart != "" && descPart != "" {
				if isValidPhaseID(idPart) {
					name = descPart
					id = slugify(idPart)
					matched = true
				}
			}
		}
	}

	if matched && name != "" {
		if id == "" {
			id = slugify(name)
		}
		if id == "" {
			id = "phase-" // will be suffixed with number by caller
		}
		return &orchestrator.DeliveryPhase{
			ID:              id,
			Title:           name,
			RequiredFiles:   []string{},
			QAVerifyCommand: "",
			SpecFocus:       name, // Will be updated with description later
		}
	}
	return nil
}

func defaultPhasesFromPaths(paths []string) []orchestrator.DeliveryPhase {
	// Flat layouts collapse to a single phase; subdirectory layouts group by dir.
	return orchestrator.PhasesFromFilePaths(paths)
}

func inferVerifyCommand(spec string, paths []string) string {
	// Look for test commands in SPEC
	lower := strings.ToLower(spec)
	if strings.Contains(lower, "go test") {
		return "cd " + inferLayoutRoot(paths) + " && go test ./..."
	}
	if strings.Contains(lower, "pytest") {
		return "cd " + inferLayoutRoot(paths) + " && pytest"
	}
	if strings.Contains(lower, "npm test") {
		return "cd " + inferLayoutRoot(paths) + " && npm test"
	}
	return ""
}

func inferLayoutRoot(paths []string) string {
	for _, p := range paths {
		parts := strings.Split(p, "/")
		if len(parts) > 1 {
			return parts[0]
		}
	}
	return "."
}

func inferTestRunner(paths []string) string {
	for _, p := range paths {
		ext := strings.ToLower(filepath.Ext(p))
		if ext == ".py" {
			return "pytest"
		}
		if ext == ".go" {
			return "go"
		}
		if ext == ".js" || ext == ".ts" {
			return "npm"
		}
	}
	return "go"
}

func slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "-")
	s = strings.ReplaceAll(s, "(", "")
	s = strings.ReplaceAll(s, ")", "")
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, ".", "-")
	return s
}

// isValidPhaseID reports whether a string looks like a valid phase identifier.
// Phase IDs in Format 6 are short, kebab-case identifiers like:
//   go-module, store-layer, api-handlers, web-static, web-shell,
//   integration-test, server-main, frontend, backend, etc.
// They are NOT sentences, don't end with punctuation, and are short (1-3 words max).
func isValidPhaseID(idPart string) bool {
	// Must be short (max 3 words)
	words := strings.Fields(idPart)
	if len(words) == 0 || len(words) > 3 {
		return false
	}
	
	// Must not end with punctuation
	if strings.HasSuffix(idPart, ".") || strings.HasSuffix(idPart, ":") || strings.HasSuffix(idPart, ";") {
		return false
	}
	
	// Must not contain action-verb-only words at the start
	// (e.g., "Create", "Implement", "Success:", "Use", "Verify")
	firstWord := strings.ToLower(words[0])
	actionPrefixes := map[string]bool{
		"create":     true, "implement": true, "write":    true,
		"set":        true, "register":  true, "success":  true,
		"use":        true, "verify":    true, "add":      true,
		"simulate":   true, "marshal":   true, "ensure":   true,
		"run":        true, "build":     true, "test":     true,
		"deploy":     true, "configure": true, "initialize": true,
		"setup":      true, "start":     true, "stop":     true,
		"check":      true, "validate":  true, "generate": true,
		"produce":    true, "execute":   true, "handle":   true,
		"process":    true, "manage":    true, "update":   true,
		"delete":     true, "remove":    true, "install":  true,
		"define":     true, "specify":   true, "document": true,
		"review":     true, "audit":     true, "monitor":  true,
	}
	if actionPrefixes[firstWord] {
		return false
	}
	
	// Must be mostly lowercase with optional dashes (kebab-case pattern)
	// Allow: go-module, store-layer, api-handlers, web-static, server-main, integration-test
	// Reject: "Create go.mod", "Success go mod tidy", "Use httptest", etc.
	hasUpper := false
	for _, r := range idPart {
		if r >= 'A' && r <= 'Z' {
			hasUpper = true
			break
		}
	}
	if hasUpper {
		return false
	}
	
	// Must contain at least one dash or be a known single-word phase
	// (go, server, web, integration, frontend, backend, api, store, module, shell, static)
	if !strings.Contains(idPart, "-") {
		singleWordPhases := map[string]bool{
			"go": true, "server": true, "web": true, "integration": true,
			"frontend": true, "backend": true, "api": true, "store": true,
			"module": true, "shell": true, "static": true, "docker": true,
			"ci": true, "build": true, "test": true, "deploy": true,
			"lint": true, "format": true, "vet": true, "cover": true,
		}
		if !singleWordPhases[idPart] {
			return false
		}
	}
	
	// Max length check
	if len(idPart) > 50 {
		return false
	}
	
	return true
}

// getEntryPointPatterns returns file suffix patterns that indicate the main
// server entry point for a given test runner.
func getEntryPointPatterns(testRunner string) []string {
	switch testRunner {
	case "go":
		return []string{"/cmd/server/main.go", "/server/main.go", "/main.go"}
	case "pytest":
		return []string{"/main.py", "/app.py", "/server.py", "/run.py"}
	case "npm":
		return []string{"/server.js", "/index.js", "/main.js", "/app.js", "/server.ts", "/index.ts"}
	default:
		return []string{"/main.go", "/main.py", "/server.js", "/index.js"}
	}
}
