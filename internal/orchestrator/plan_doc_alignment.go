package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/llm"
)

var (
	goModModuleLineRE     = regexp.MustCompile(`(?m)^module\s+(\S+)`)
	planWrongModuleRE     = regexp.MustCompile(`(?i)(?:^|\s)module\s+(?:github\.com/)?example\b|github\.com/example`)
	storeHallucinationREs = []*regexp.Regexp{
		regexp.MustCompile(`\bListLinks\b`),
		regexp.MustCompile(`\bCreateLink\b`),
		regexp.MustCompile(`\bDeleteLink\b`),
		regexp.MustCompile(`\bGetLinks\b`),
		regexp.MustCompile(`\bNewStore\s*\(`),
		regexp.MustCompile(`\btype\s+Store\s+struct\b`),
	}
	planMandatoryTestRE = []*regexp.Regexp{
		regexp.MustCompile(`(?i)httptest\s+(?:is\s+)?(?:required|mandatory)`),
		regexp.MustCompile(`(?i)eslint\s+(?:is\s+)?(?:required|mandatory)`),
		regexp.MustCompile(`(?i)(?:unit|integration)\s+tests?\s+must\b`),
		regexp.MustCompile(`(?i)mandatory\s+.*_test\.go`),
		regexp.MustCompile(`(?i)every\s+bead\s+must\s+include\s+.*_test\.go`),
	}
	integrationContractHeadingRE = regexp.MustCompile(`(?im)^##\s+integration\s+contract\b`)
	planBeadSectionPathRE        = regexp.MustCompile(`(?m)^###\s+([a-zA-Z0-9][a-zA-Z0-9_-]*):\s*(.+)$`)
	bareModuleRelPathRE          = regexp.MustCompile(`(?:^|[\s\-*` + "`" + `])(?:\./)?((?:internal|cmd|pkg|api|web)/[^\s` + "`" + `",:;)]+)`)
	dockerDeploymentHeadingRE    = regexp.MustCompile(`(?im)^##\s+docker\s*(?:&|and)\s*deployment\b`)
)

// WriteAlignedPlanningDocsForTest writes minimal SPEC/architecture/plan stubs for gt-agent tests.
func WriteAlignedPlanningDocsForTest(rigDir string) error {
	spec := "# Test SPEC\n"
	arch := "# Test architecture\nAligned with SPEC.\n"
	plan := "# Test plan\n## Bead map\n### test-1: main.go\n- Scope: test\n"
	for name, body := range map[string]string{"SPEC.md": spec, "architecture.md": arch, "plan.md": plan} {
		if err := os.WriteFile(filepath.Join(rigDir, name), []byte(body), 0644); err != nil {
			return err
		}
	}
	return nil
}

func formatDocAlignmentError(prefix string, issues []string) error {
	if len(issues) == 0 {
		return nil
	}
	if len(issues) > 10 {
		issues = append(issues[:10], fmt.Sprintf("…and %d more", len(issues)-10))
	}
	return fmt.Errorf("%s: %s", prefix, strings.Join(issues, "; "))
}

// ValidateArchitectureDocAlignment ensures architecture.md matches SPEC.md (HTTP routes, store API, module).
// Called on design success so the planner is not blocked by architect drift.
func ValidateArchitectureDocAlignment(rigDir string, v WorkflowValidation) error {
	specDoc := readRigDoc(rigDir, "SPEC.md")
	if strings.TrimSpace(specDoc) == "" {
		return fmt.Errorf("SPEC.md missing or empty under %s", rigDir)
	}
	issues := architectureDocAlignmentIssues(rigDir, specDoc, v)
	return formatDocAlignmentError("SPEC/architecture misaligned", issues)
}

func architectureDocAlignmentIssues(rigDir, specDoc string, v WorkflowValidation) []string {
	archDoc := readRigDoc(rigDir, "architecture.md")
	return architectureDocAlignmentIssuesForDoc(archDoc, specDoc, v, true)
}

func architectureDocAlignmentIssuesForDoc(archDoc, specDoc string, v WorkflowValidation, includeContentChecks bool) []string {
	var issues []string
	issues = append(issues, checkArchitectureStoreSignatureDrift("architecture.md", archDoc, specDoc)...)
	issues = append(issues, checkHTTPDocAlignment("architecture.md", archDoc, specDoc, v)...)
	issues = append(issues, checkStoreAPIAlignment("architecture.md", archDoc, specDoc)...)
	issues = append(issues, checkGoModuleAlignment("architecture.md", archDoc, specDoc, v)...)
	issues = append(issues, checkDocLayoutPathPrefix("architecture.md", archDoc, v)...)
	if includeContentChecks {
		issues = append(issues, checkArchitectureDockerSection(archDoc, v)...)
		issues = append(issues, checkArchitectureIntegrationTestingSection(archDoc, v)...)
		issues = append(issues, checkArchitectureE2ETestingSection(archDoc, v)...)
	}
	return issues
}

// PlanningDocsMisaligned reports whether SPEC/architecture/plan fail alignment for the active profile.
// Used by planning sync refresh and workflow-stuck repair (layout_root-prefixed required_files).
// When plan.md is missing, also checks architecture-vs-SPEC alignment — a hallucinated
// architecture.md can prevent plan.md generation, and the stuck detector must catch this.
func PlanningDocsMisaligned(rigDir string, v WorkflowValidation) bool {
	rigDir = strings.TrimSpace(rigDir)
	if rigDir == "" {
		return false
	}
	v = v.ForActivePhase()
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" || layout == "." || !profileRequiredFilesUseLayoutPrefix(v, layout) {
		return false
	}
	planMissing := false
	if _, err := os.Stat(filepath.Join(rigDir, "plan.md")); err != nil {
		planMissing = true
	}
	if !planMissing {
		return ValidatePlanningDocAlignment(rigDir, v) != nil
	}
	return ValidateArchitectureDocAlignment(rigDir, v) != nil
}

// ValidatePlanningDocAlignment ensures SPEC.md, architecture.md, and plan.md agree on HTTP routes,
// store API names, and module identity before project_setup / implementation.
func ValidatePlanningDocAlignment(rigDir string, v WorkflowValidation) error {
	specDoc := readRigDoc(rigDir, "SPEC.md")
	planDoc := readRigDoc(rigDir, "plan.md")
	if strings.TrimSpace(specDoc) == "" {
		return fmt.Errorf("SPEC.md missing or empty under %s", rigDir)
	}

	archDoc := readRigDoc(rigDir, "architecture.md")
	var issues []string
	issues = append(issues, architectureDocAlignmentIssuesForDoc(archDoc, specDoc, v, false)...)
	issues = append(issues, checkHTTPDocAlignment("plan.md", planDoc, specDoc, v)...)
	issues = append(issues, checkStoreAPIAlignment("plan.md", planDoc, specDoc)...)
	issues = append(issues, checkGoModuleAlignment("plan.md", planDoc, specDoc, v)...)
	issues = append(issues, checkPlanTestMandate(planDoc, v)...)
	issues = append(issues, checkPlanIntegrationContract(planDoc, specDoc, v)...)
	rigName := filepath.Base(filepath.Dir(filepath.Dir(rigDir)))
	issues = append(issues, checkPlanBeadMapExactPaths(planDoc, v, rigName)...)
	issues = append(issues, checkDocLayoutPathPrefix("plan.md", planDoc, v)...)

	// Semantic triad validation using LLM judge
	// Only run if all three documents have substantive content (>200 chars each)
	const triadMinLength = 200
	if len(strings.TrimSpace(specDoc)) >= triadMinLength &&
		len(strings.TrimSpace(archDoc)) >= triadMinLength &&
		len(strings.TrimSpace(planDoc)) >= triadMinLength {
		client := llm.NewClient(
			"http://localhost:11434/v1/chat/completions",
			GetModel("judge"),
			"",
			120*time.Second,
		)
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		pass, reason, err := ValidateTriadWithJudge(ctx, client, TriadValidationConfig{
			SPEC:         specDoc,
			Architecture: archDoc,
			Plan:         planDoc,
			MinLength:    200,
		})
		if err != nil {
			log.Printf("[triad] LLM judge unavailable, skipping semantic validation: %v", err)
		} else if !pass {
			// Log triad findings for observability but don't block the transition —
			// semantic misalignments (e.g., different file sets in architecture vs plan)
			// cannot be fixed by the planner (which only manages beads for required_files)
			// and are better surfaced to plan_review or QA for triage.
			log.Printf("[triad] advisory: SPEC/Architecture/Plan triad misaligned: %s", reason)
		}
	}

	return formatDocAlignmentError("SPEC/architecture/plan misaligned", issues)
}

// checkArchitectureDockerSection requires a substantive ## Docker & Deployment section
// when the workflow uses Docker (Dockerfile in required_files or docker in QA command).
func checkArchitectureDockerSection(archDoc string, v WorkflowValidation) []string {
	if !WorkflowUsesDocker(v) {
		return nil
	}
	loc := dockerDeploymentHeadingRE.FindStringIndex(archDoc)
	if loc == nil {
		return []string{"architecture.md must have a ## Docker & Deployment section with base images, build steps, exposed port, and CMD (Docker files are in profile)"}
	}
	section := extractMarkdownSection(archDoc, loc[0])
	if len(strings.TrimSpace(section)) < 200 {
		return []string{"## Docker & Deployment section is too brief; add base images, build steps, exposed port, and CMD"}
	}

	// Use LLM judge for deeper validation
	client := llm.NewClient(
		"http://localhost:11434/v1/chat/completions",
		GetModel("judge"),
		"",
		60*time.Second,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pass, reason, err := ValidateDocumentWithJudge(ctx, client, JudgeConfig{
		DocumentName: "architecture.md (## Docker & Deployment section)",
		Content:      archDoc,
		Criteria: []string{
			"Documents the base images used in the Dockerfile (e.g., node:20-slim, python:3.12-slim)",
			"Describes the multi-stage build steps clearly",
			"Specifies the exposed port (e.g., 8000)",
			"Documents the CMD/entrypoint used to run the server",
		},
		MinLength: 200,
	})
	if err != nil {
		log.Printf("[judge] LLM unavailable for Docker section validation: %v", err)
		return nil
	}
	if !pass {
		return []string{fmt.Sprintf("## Docker & Deployment section failed judge: %s", reason)}
	}
	return nil
}

// hasTestRequiredFiles reports whether the profile explicitly lists test files
// (e.g. *_test.go, *.test.*, files under a test/ directory).
func hasTestRequiredFiles(v WorkflowValidation) bool {
	for _, f := range v.UnionRequiredFiles() {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		if strings.Contains(lower, "_test.") || strings.Contains(lower, ".test.") ||
			strings.Contains(lower, "/test/") || strings.Contains(lower, "/tests/") ||
			strings.Contains(lower, "test_") {
			return true
		}
		base := filepath.Base(lower)
		switch base {
		case "conftest.py", "pytest.ini", "jest.config.js", "jest.config.ts",
			"vitest.config.ts", "vitest.config.js", "karma.conf.js":
			return true
		}
	}
	return false
}

// checkArchitectureIntegrationTestingSection ensures the ## Integration and testing section
// is present and contains substantive content when the profile lists test files in required_files
// or the QA verify command runs tests.
func checkArchitectureIntegrationTestingSection(archDoc string, v WorkflowValidation) []string {
	if !hasTestRequiredFiles(v) && !strings.Contains(strings.ToLower(v.QAVerifyCommand), "test") {
		return nil
	}
	loc := integrationTestingHeadingRE.FindStringIndex(archDoc)
	if loc == nil {
		return []string{"architecture.md must have an ## Integration and testing section with test strategy, unit test structure, and runtime smoke test"}
	}
	section := extractMarkdownSection(archDoc, loc[0])
	if strings.TrimSpace(section) == "" {
		return []string{"## Integration and testing section is empty; add test strategy, unit test structure, and runtime smoke test"}
	}
	// Require substantive content.
	if len(strings.TrimSpace(section)) < 200 {
		return []string{"## Integration and testing section is too brief; add test strategy, unit test structure, and runtime smoke test"}
	}
	return nil
}

// checkArchitectureE2ETestingSection ensures the ## E2E / integration testing section
// is present and contains substantive content when the profile lists e2e/docker-compose files.
func checkArchitectureE2ETestingSection(archDoc string, v WorkflowValidation) []string {
	hasE2EFiles := false
	for _, f := range v.UnionRequiredFiles() {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(f)))
		if strings.Contains(lower, "e2e") || strings.Contains(lower, "playwright") ||
			strings.Contains(lower, "docker-compose") || strings.Contains(lower, "docker_compose") {
			hasE2EFiles = true
			break
		}
	}
	if !hasE2EFiles {
		return nil
	}
	loc := e2eTestingHeadingRE.FindStringIndex(archDoc)
	if loc == nil {
		return []string{"architecture.md must have an ## E2E / integration testing section when e2e/playwright/docker-compose files are in required_files"}
	}
	section := extractMarkdownSection(archDoc, loc[0])
	if strings.TrimSpace(section) == "" {
		return []string{"## E2E / integration testing section is empty; add how app under test is started, how e2e tests are executed, what they cover, and test data/env requirements"}
	}
	// Require substantive content.
	if len(strings.TrimSpace(section)) < 200 {
		return []string{"## E2E / integration testing section is too brief; add how app under test is started, how e2e tests are executed, what they cover, and test data/env requirements"}
	}
	return nil
}

var integrationTestingHeadingRE = regexp.MustCompile(`(?im)^##\s+Integration\s+and\s+testing\b`)
var e2eTestingHeadingRE = regexp.MustCompile(`(?im)^##\s+E2E\s*/\s*integration\s+testing\b`)

func extractMarkdownSection(doc string, headingStart int) string {
	rest := doc[headingStart:]
	lines := strings.Split(rest, "\n")
	if len(lines) == 0 {
		return ""
	}
	var out []string
	for i, line := range lines {
		if i == 0 {
			continue // skip heading line
		}
		// Only break on same-level (## ) or higher (# ) headings, not subheadings (###)
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") || strings.HasPrefix(trimmed, "# ") {
			break
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// checkDocLayoutPathPrefix rejects bare module-relative paths (internal/..., cmd/...) when the
// workflow profile lists required_files under layout_root/ (e.g. linkshelf/internal/store/...).
func checkDocLayoutPathPrefix(docName, doc string, v WorkflowValidation) []string {
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(v.LayoutRoot)), "/")
	if layout == "" || layout == "." || strings.TrimSpace(doc) == "" {
		return nil
	}
	if !profileRequiredFilesUseLayoutPrefix(v, layout) {
		return nil
	}
	var issues []string
	for _, p := range extractDocImplementPaths(doc, layout) {
		if !needsLayoutPathPrefix(p, layout) {
			continue
		}
		want := layout + "/" + strings.TrimPrefix(filepath.ToSlash(p), "/")
		issues = append(issues, fmt.Sprintf("%s references %q; use %q to match workflow required_files under layout_root", docName, p, want))
	}
	return dedupeStrings(issues)
}

func profileRequiredFilesUseLayoutPrefix(v WorkflowValidation, layout string) bool {
	for _, f := range v.UnionRequiredFiles() {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if strings.HasPrefix(f, layout+"/") {
			return true
		}
	}
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if strings.HasPrefix(f, layout+"/") {
			return true
		}
	}
	return false
}

func needsLayoutPathPrefix(p, layout string) bool {
	p = filepath.ToSlash(strings.TrimSpace(p))
	layout = strings.Trim(filepath.ToSlash(strings.TrimSpace(layout)), "/")
	if p == "" || layout == "" {
		return false
	}
	if strings.HasPrefix(p, layout+"/") || p == layout {
		return false
	}
	if p == "go.mod" || p == "go.sum" {
		return true
	}
	for _, pre := range []string{"internal/", "cmd/", "pkg/", "api/", "web/"} {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

func extractDocImplementPaths(doc, layoutRoot string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || seen[p] || !isLikelyRepoFilePath(p, layoutRoot) {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range extractArchPaths(doc, layoutRoot) {
		add(p)
	}
	for _, m := range planBeadSectionPathRE.FindAllStringSubmatch(doc, -1) {
		if len(m) >= 2 {
			add(m[1])
		}
	}
	for _, m := range bareModuleRelPathRE.FindAllStringSubmatch(doc, -1) {
		if len(m) >= 2 && pathNeedsLayoutPrefixCheck(m[1]) {
			add(m[1])
		}
	}
	return out
}

// pathNeedsLayoutPrefixCheck limits layout_root prefix lint to implement file paths (e.g.
// linkshelf/internal/store/store.go), not package qualifiers (internal/store.List) or
// directory fragments (cmd/server from "go run ./cmd/server").
func pathNeedsLayoutPrefixCheck(p string) bool {
	p = filepath.ToSlash(strings.TrimSpace(p))
	if p == "" {
		return false
	}
	if p == "go.mod" || p == "go.sum" {
		return true
	}
	// Skip Go test patterns (internal/... or ./internal/...)
	if strings.HasSuffix(p, "/...") || strings.HasSuffix(p, "/...") || strings.Contains(p, "/...") {
		return false
	}
	return strings.Contains(filepath.Base(p), ".")
}

func checkHTTPDocAlignment(docName, doc, specDoc string, v WorkflowValidation) []string {
	if strings.TrimSpace(doc) == "" {
		return nil
	}
	specAPI := parseAPISmokeSpecText(specDoc, v)
	specPaths := apiPathSet(specAPI)
	if len(specPaths) == 0 {
		return nil
	}
	docAPI := parseAPISmokeSpecText(doc, v)
	var issues []string
	for _, p := range docAPI.GETPaths {
		if issue := httpPathDriftIssue(docName, p, specPaths); issue != "" {
			issues = append(issues, issue)
		}
	}
	for _, probe := range docAPI.POSTProbes {
		if issue := httpPathDriftIssue(docName, probe.Path, specPaths); issue != "" {
			issues = append(issues, issue)
		}
	}
	return issues
}

func apiPathSet(api APISmokeSpec) map[string]bool {
	out := make(map[string]bool)
	for _, p := range api.GETPaths {
		out[p] = true
	}
	for _, probe := range api.POSTProbes {
		out[probe.Path] = true
	}
	return out
}

func httpPathDriftIssue(docName, path string, specPaths map[string]bool) string {
	path = normalizeSmokePath(path)
	if path == "" {
		return ""
	}
	if specPaths[path] {
		return ""
	}
	if conflictingCanonicalPath(path, specPaths) != "" {
		return fmt.Sprintf("%s uses %s but SPEC routes are %s", docName, path, conflictingCanonicalPath(path, specPaths))
	}
	return fmt.Sprintf("%s documents HTTP path %s not present in SPEC (canonical: %s)", docName, path, joinSortedPathKeys(specPaths))
}

// conflictingCanonicalPath returns a SPEC path that shares the same resource suffix (e.g. /links vs /api/links).
func conflictingCanonicalPath(path string, specPaths map[string]bool) string {
	bare := strings.TrimPrefix(path, "/")
	var matches []string
	for sp := range specPaths {
		if sp == path {
			continue
		}
		if strings.HasSuffix(sp, "/"+bare) || strings.HasSuffix(path, strings.TrimPrefix(sp, "/")) {
			matches = append(matches, sp)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return strings.Join(matches, ", ")
}

func joinSortedPathKeys(paths map[string]bool) string {
	var keys []string
	for k := range paths {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func checkStoreAPIAlignment(docName, doc, specDoc string) []string {
	if strings.TrimSpace(doc) == "" {
		return nil
	}
	canonical := canonicalStoreSymbolsFromSPEC(specDoc)
	if len(canonical) == 0 {
		return nil
	}
	canonList := strings.Join(sortedKeys(canonical), ", ")
	var issues []string
	for _, re := range storeHallucinationREs {
		if m := re.FindString(doc); m != "" {
			issues = append(issues, fmt.Sprintf("%s uses %s; SPEC store API: %s", docName, m, canonList))
		}
	}
	for _, alt := range storeSymbolAlternatives(canonical) {
		if wordInDoc(doc, alt) && isForbiddenStoreAlias(alt, canonical) {
			issues = append(issues, fmt.Sprintf("%s uses %s; SPEC store API: %s", docName, alt, canonList))
		}
	}
	return dedupeStrings(issues)
}

// isForbiddenStoreAlias reports wrong store API names. Package-qualified forms like
// store.List or schema.InitSchema are allowed when the base symbol is in SPEC.
func isForbiddenStoreAlias(name string, canonical map[string]bool) bool {
	if canonical[name] {
		return false
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		if canonical[strings.TrimSpace(name[i+1:])] {
			return false
		}
	}
	return true
}

func canonicalStoreSymbolsFromSPEC(specDoc string) map[string]bool {
	var chunks []string
	for _, heading := range []string{"Store", "Data model", "HTTP", "API"} {
		if s := ExtractSpecMarkdownSection(specDoc, heading); s != "" {
			chunks = append(chunks, s)
		}
	}
	for _, block := range goCodeFenceRE.FindAllStringSubmatch(specDoc, -1) {
		if len(block) >= 2 {
			chunks = append(chunks, block[1])
		}
	}
	seen := map[string]bool{}
	for _, chunk := range chunks {
		for _, sym := range extractContractSymbolsFromGoSource(chunk) {
			seen[sym] = true
		}
	}
	return seen
}

func storeSymbolAlternatives(canonical map[string]bool) []string {
	altMap := map[string][]string{
		"List":       {"ListLinks", "GetLinks"},
		"Create":     {"CreateLink", "AddLink"},
		"Delete":     {"DeleteLink", "RemoveLink"},
		"InitSchema": {"InitDB", "Migrate"},
	}
	var out []string
	for canon, alts := range altMap {
		if !canonical[canon] {
			continue
		}
		out = append(out, alts...)
	}
	return out
}

func wordInDoc(doc, word string) bool {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(word) + `\b`)
	return re.FindStringIndex(doc) != nil
}

func checkGoModuleAlignment(docName, doc, specDoc string, v WorkflowValidation) []string {
	if strings.TrimSpace(doc) == "" {
		return nil
	}
	canonical := canonicalGoModule(specDoc, v)
	var issues []string
	if planWrongModuleRE.FindStringIndex(doc) != nil {
		issues = append(issues, fmt.Sprintf("%s uses placeholder module path (example/...) — SPEC/module is %q", docName, canonical))
	}
	if canonical == "" {
		return issues
	}
	if m := goModModuleLineRE.FindStringSubmatch(doc); len(m) >= 2 && m[1] != canonical {
		issues = append(issues, fmt.Sprintf("%s declares module %s but SPEC expects %s", docName, m[1], canonical))
	}
	return issues
}

func canonicalGoModule(specDoc string, v WorkflowValidation) string {
	if m := goModModuleLineRE.FindStringSubmatch(specDoc); len(m) >= 2 {
		return m[1]
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout != "" && layout != "." {
		return layout
	}
	return ""
}

func checkPlanTestMandate(planDoc string, v WorkflowValidation) []string {
	if strings.TrimSpace(planDoc) == "" || profileRequiresTestBeads(v) {
		return nil
	}
	var issues []string
	for _, re := range planMandatoryTestRE {
		if loc := re.FindStringIndex(planDoc); loc != nil {
			issues = append(issues, fmt.Sprintf("plan.md mandates tests (%q) but active phase required_files has no *_test.go — match SPEC/MVP scope", planDoc[loc[0]:loc[1]]))
			break
		}
	}
	return issues
}

func profileRequiresTestBeads(v WorkflowValidation) bool {
	v = v.ForActivePhase()
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(f)
		if strings.HasSuffix(f, "_test.go") || strings.Contains(f, "/tests/test_") || strings.HasSuffix(f, "_test.py") {
			return true
		}
	}
	return false
}

// checkArchitectureStoreSignatureDrift rejects architecture store signatures that use *sql.DB
// receivers/params when SPEC documents package-level context.Context APIs.
func checkArchitectureStoreSignatureDrift(docName, archDoc, specDoc string) []string {
	if strings.TrimSpace(archDoc) == "" || strings.TrimSpace(specDoc) == "" {
		return nil
	}
	specWantsCtx := strings.Contains(specDoc, "context.Context") &&
		(strings.Contains(specDoc, "package-level") || strings.Contains(specDoc, "var DB *sql.DB") || strings.Contains(specDoc, "store.DB"))
	if !specWantsCtx {
		return nil
	}
	var issues []string
	if strings.Contains(archDoc, "func List(db *sql.DB)") || strings.Contains(archDoc, "func List(*sql.DB)") {
		issues = append(issues, fmt.Sprintf("%s store API uses List(db *sql.DB); SPEC requires package-level List(ctx context.Context)", docName))
	}
	if strings.Contains(archDoc, "func Create(db *sql.DB") || strings.Contains(archDoc, "Create(db *sql.DB, link") {
		issues = append(issues, fmt.Sprintf("%s store API uses Create(db *sql.DB, …); SPEC requires Create(ctx context.Context, title, url string)", docName))
	}
	if strings.Contains(archDoc, "func Delete(db *sql.DB") {
		issues = append(issues, fmt.Sprintf("%s store API uses Delete(db *sql.DB, …); SPEC requires Delete(ctx context.Context, id int64)", docName))
	}
	return issues
}

func checkPlanIntegrationContract(planDoc, specDoc string, v WorkflowValidation) []string {
	if strings.TrimSpace(planDoc) == "" || !profileHasServerEntrypoint(v) {
		return nil
	}
	contract := ExtractSpecMarkdownSection(planDoc, "Integration contract")
	if contract == "" {
		return []string{"plan.md missing ## Integration contract (entrypoint bead in profile — wire order, route table, exported symbols per SPEC)"}
	}
	if !RequiresExactImplementPaths(v) {
		return nil
	}
	routes := parseSpecHTTPRouteTable(specDoc)
	if len(routes) == 0 {
		return nil
	}
	var issues []string
	for _, row := range routes {
		if !strings.Contains(contract, row.Path) {
			issues = append(issues, fmt.Sprintf("plan.md integration contract must include SPEC route %q (got truncated paths like /static or /api/links without path params)", row.Path))
		}
	}
	return issues
}

// extractPlanBeadMapPath parses the file path from a ### <id>: … plan.md bead-map header.
func extractPlanBeadMapPath(sectionLine string) string {
	m := planBeadSectionPathRE.FindStringSubmatch(sectionLine)
	if len(m) < 3 {
		return ""
	}
	rest := strings.TrimSpace(m[2])
	if before, _, ok := strings.Cut(rest, " per architecture"); ok {
		rest = strings.TrimSpace(before)
	}
	if before, _, ok := strings.Cut(rest, " per arch"); ok {
		rest = strings.TrimSpace(before)
	}
	if strings.HasPrefix(strings.ToLower(rest), "implement ") {
		rest = strings.TrimSpace(rest[len("implement "):])
	}
	return filepath.ToSlash(strings.TrimSpace(rest))
}

// checkPlanBeadMapExactPaths rejects ### bead-map paths that are not exact required_files entries.
func checkPlanBeadMapExactPaths(planDoc string, v WorkflowValidation, rig string) []string {
	if !RequiresExactImplementPaths(v) || strings.TrimSpace(planDoc) == "" {
		return nil
	}
	v = v.ForActivePhase()
	normalize := func(p string) string {
		if rig != "" {
			return NormalizePlannerBeadPath(p, v.LayoutRoot, rig)
		}
		return filepath.ToSlash(strings.TrimSpace(p))
	}
	expected := make(map[string]bool)
	for _, f := range v.RequiredFiles {
		if p := normalize(f); p != "" {
			expected[p] = true
		}
	}
	var issues []string
	for _, line := range strings.Split(planDoc, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "### ") {
			continue
		}
		p := normalize(extractPlanBeadMapPath(line))
		if p == "" || expected[p] {
			continue
		}
		issues = append(issues, fmt.Sprintf("plan.md bead map uses %q; required_files expects full path (e.g. under %s/internal/ or %s/cmd/)", p, v.LayoutRoot, v.LayoutRoot))
	}
	return issues
}

func profileHasServerEntrypoint(v WorkflowValidation) bool {
	v = v.ForActivePhase()
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(f)
		if strings.Contains(f, "/cmd/") && strings.HasSuffix(f, "main.go") {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var planningArchitectRedirectErrors = []string{
	"misaligned",
	"alignment",
	"architecture.md",
}

// planningSyncNeedsArchitect reports whether a SyncPlanningArtifacts failure means
// architecture.md must be revised (plan.md cannot be generated until architecture matches SPEC).
func planningSyncNeedsArchitect(err error, townRoot, rig string, v WorkflowValidation) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	hasAlignment := false
	for _, needle := range planningArchitectRedirectErrors {
		if strings.Contains(msg, needle) {
			hasAlignment = true
			break
		}
	}
	if !hasAlignment || townRoot == "" || rig == "" {
		return false
	}
	planPath := filepath.Join(townRoot, rig, "mayor", "rig", "plan.md")
	_, statErr := os.Stat(planPath)
	return statErr != nil
}

// planningGateNeedsArchitect reports whether a ValidatePlanningPhaseGate failure
// means architecture.md is the root cause (not bead/plan.md issues that the planner can fix).
func planningGateNeedsArchitect(err error, rig string, v WorkflowValidation, fromState string) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if !strings.Contains(msg, "misaligned") && !strings.Contains(msg, "alignment") {
		return false
	}
	if strings.Contains(msg, "plan.md") && !strings.Contains(msg, "architecture.md") {
		return false
	}
	return true
}
