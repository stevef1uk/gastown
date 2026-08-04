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

	endpoint, model := ResolveLLMForSpecIndex(townRoot)

	// Build phase ID list for the LLM
	phaseIDs := make([]string, len(phases))
	for i, p := range phases {
		phaseIDs[i] = p.ID
	}

	system := `You assign files to delivery phases. Input: a SPEC, a list of phase IDs, and an EXACT list of ALL file paths that MUST be covered.
Output a single JSON object mapping phase_id -> list of file paths from the input file list ONLY.
Rules:
- Every file from the input file list MUST appear exactly once across all phases.
- Do NOT add files not in the input list. Do NOT omit files.
- Only output the JSON mapping. No prose.`

	// Prepare file list for prompt
	fileList := strings.Join(files, "\n")
	phaseList := strings.Join(phaseIDs, ", ")

	user := fmt.Sprintf(`SPEC:
%s

PHASES: %s
FILES (must all be assigned exactly once):
%s

Return JSON: { "phase-id": ["file1", "file2"], ... }`, spec, phaseList, fileList)

	content, err := chatCompletionJSON(ctx, endpoint, model, system, user)
	if err != nil {
		log.Printf("[deterministic-index] LLM phase assignment call failed: %v", err)
		return phases, false
	}

	// Strip markdown code fences if LLM wraps JSON in ```
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		lines := strings.Split(content, "\n")
		if len(lines) > 2 {
			content = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}

	var phaseFiles map[string][]string
	if err := json.Unmarshal([]byte(content), &phaseFiles); err != nil {
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

	return assigned, true
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
		return parsePhaseList(section)
	}
	return nil
}

func parsePhaseList(section string) []orchestrator.DeliveryPhase {
	lines := strings.Split(section, "\n")
	var phases []orchestrator.DeliveryPhase
	var current *orchestrator.DeliveryPhase
	phaseNum := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "```") {
			continue
		}
		// Numbered list: "1. **phase-name** — description" or "- **phase-name**"
		if strings.Contains(trimmed, "**") && (strings.HasPrefix(trimmed, "-") || (len(trimmed) > 1 && trimmed[0] >= '1' && trimmed[0] <= '9')) {
			// Extract phase name from **...**
			start := strings.Index(trimmed, "**")
			end := strings.Index(trimmed[start+2:], "**")
			if start >= 0 && end >= 0 {
				name := trimmed[start+2 : start+2+end]
				phaseNum++
				current = &orchestrator.DeliveryPhase{
					ID:              slugify(name),
					Title:           name,
					RequiredFiles:   []string{},
					QAVerifyCommand: "",
					SpecFocus:       "",
				}
				phases = append(phases, *current)
			}
		} else if current != nil && (strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "*")) {
			// File entry under phase - skip for now (we use tree paths)
		}
	}
	return phases
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
