package rtm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/beads"
	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/orchestrator"
	"github.com/steveyegge/gastown/internal/style"
	"github.com/steveyegge/gastown/internal/ui"
)

// AuditStatus represents the verification outcome for a single requirement row.
type AuditStatus string

const (
	AuditPass    AuditStatus = "PASS"
	AuditWarn    AuditStatus = "WARN"
	AuditFail    AuditStatus = "FAIL"
	AuditPending AuditStatus = "PENDING"
)

// RTMRow represents a single requirement's traceability link across SPEC, Bead, Test, and Status.
type RTMRow struct {
	ReqID        string      `json:"req_id"`
	Requirement  string      `json:"requirement"`
	Phase        string      `json:"phase,omitempty"`
	Level        string      `json:"level,omitempty"`
	BeadID       string      `json:"bead_id,omitempty"`
	BeadStatus   string      `json:"bead_status,omitempty"`
	BeadTitle    string      `json:"bead_title,omitempty"`
	TestFile     string      `json:"test_file,omitempty"`
	TestExists   bool        `json:"test_exists"`
	TestBytes    int64       `json:"test_bytes,omitempty"`
	Scenarios    []string    `json:"scenarios,omitempty"`
	Assertions   []string    `json:"assertions,omitempty"`
	AuditStatus  AuditStatus `json:"audit_status"`
	AuditMessage string      `json:"audit_message,omitempty"`
}

// RTMBeadRow represents an implementation bead linked to a requirement or delivery artifact.
type RTMBeadRow struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Category    string `json:"category"`
	Artifact    string `json:"artifact,omitempty"`
	Requirement string `json:"requirement,omitempty"`
	Phase       string `json:"phase,omitempty"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Purpose     string `json:"purpose,omitempty"`
}

// RTMSummary captures the categorized inventory of delivery beads for business reporting.
type RTMSummary struct {
	TotalBeads     int `json:"total_beads"`
	Requirements   int `json:"requirements"`
	Components     int `json:"components"`
	Infrastructure int `json:"infrastructure"`
	Build          int `json:"build"`
	DocsConfig     int `json:"docs_config"`
	Testing        int `json:"testing"`
}

// Category constants for business reporting
const (
	CategoryRequirement    = "Requirement"
	CategoryComponentWeb   = "Component (Web)"
	CategoryComponentData  = "Component (Data)"
	CategoryComponentUI    = "Component (UI)"
	CategoryComponentCore  = "Component (Core)"
	CategoryTesting        = "Testing"
	CategoryInfrastructure = "Infrastructure"
	CategoryBuild          = "Build & Tooling"
	CategoryDocsConfig     = "Docs & Config"
)

// RTMReport holds the complete Requirements Traceability Matrix for a rig.
type RTMReport struct {
	Rig         string       `json:"rig"`
	GeneratedAt time.Time    `json:"generated_at"`
	ActivePhase string       `json:"active_phase,omitempty"`
	Rows        []RTMRow     `json:"rows"`
	Beads       []RTMBeadRow `json:"beads,omitempty"`
	Summary     RTMSummary   `json:"summary"`
	TotalReqs   int          `json:"total_requirements"`
	Passed      int          `json:"passed"`
	Warnings    int          `json:"warnings"`
	Pending     int          `json:"pending"`
	Failures    int          `json:"failures"`
	ScorePct    float64      `json:"score_percent"`
}

var reqHeadingRE = regexp.MustCompile(`(?m)^###\s+(\S.*)$`)

// ParsedBlock contains the raw parsed fields from TEST_PLAN.md.
type ParsedBlock struct {
	ReqID       string
	Requirement string
	Level       string
	TestFile    string
	BeadID      string
	Phase       string
	Scenarios   []string
	Assertions  []string
}

// ParseTestPlanDetailed parses TEST_PLAN.md into detailed ParsedBlocks.
func ParseTestPlanDetailed(content string) []ParsedBlock {
	var blocks []ParsedBlock
	var cur *ParsedBlock
	mode := "" // "", "scenarios", "assertions"

	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)

		if m := reqHeadingRE.FindStringSubmatch(trimmed); m != nil {
			if cur != nil {
				blocks = append(blocks, *cur)
			}
			cur = &ParsedBlock{ReqID: strings.TrimSpace(m[1])}
			mode = ""
			continue
		}

		if cur == nil {
			continue
		}

		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "- ") && mode != "" {
			item := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if mode == "scenarios" {
				cur.Scenarios = append(cur.Scenarios, item)
			} else if mode == "assertions" {
				cur.Assertions = append(cur.Assertions, item)
			}
			continue
		}

		idx := strings.Index(trimmed, ":")
		if idx <= 0 {
			continue
		}

		key := strings.ToLower(strings.TrimSpace(trimmed[:idx]))
		val := strings.TrimSpace(trimmed[idx+1:])

		switch key {
		case "requirement":
			if cur.Requirement != "" {
				blocks = append(blocks, *cur)
				cur = &ParsedBlock{
					ReqID: cur.ReqID,
					Phase: cur.Phase,
				}
			}
			cur.Requirement = val
			mode = ""
		case "level":
			cur.Level = val
			mode = ""
		case "test file":
			cur.TestFile = val
			mode = ""
		case "bead id", "bead":
			cur.BeadID = val
			mode = ""
		case "phase":
			cur.Phase = val
			mode = ""
		case "scenarios":
			mode = "scenarios"
		case "assertions":
			mode = "assertions"
		default:
			mode = ""
		}
	}

	if cur != nil && (cur.Requirement != "" || cur.TestFile != "") {
		blocks = append(blocks, *cur)
	}

	return blocks
}

// BeadInfo holds metadata about an existing bead in the rig database.
type BeadInfo struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	IssueType   string   `json:"issue_type,omitempty"`
	Status      string   `json:"status"`
	CloseReason string   `json:"close_reason,omitempty"`
	Labels      []string `json:"labels,omitempty"`
}

// LoadRigBeads loads all beads for the given rig.
func LoadRigBeads(townRoot, rig string) (map[string]BeadInfo, map[string]string) {
	byID := make(map[string]BeadInfo)
	byPath := make(map[string]string) // normalized path -> beadID

	beadsDir := config.ResolveBeadsDirForRig(townRoot, rig)
	if _, err := os.Stat(beadsDir); err != nil {
		return byID, byPath
	}

	args := beads.InjectFlatForListJSON([]string{"list", "--all", "--json", "--limit=0"})
	cmd := exec.Command("bd", args...)
	cmd.Dir = filepath.Dir(beadsDir)
	cmd.Env = append(os.Environ(),
		"BEADS_DIR="+beadsDir,
		"BEADS_DOLT_AUTO_START=0",
		"BEADS_DOLT_SERVER_HOST=127.0.0.1",
		"BEADS_DOLT_SERVER_PORT=3307",
		"BEADS_DOLT_PORT=3307",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return byID, byPath
	}
	out = beads.StripStdoutWarnings(out)

	var rows []struct {
		ID          string   `json:"id"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		IssueType   string   `json:"issue_type"`
		Status      string   `json:"status"`
		CloseReason string   `json:"close_reason"`
		Labels      []string `json:"labels"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return byID, byPath
	}

	for _, r := range rows {
		id := strings.TrimSpace(beads.ExtractIssueID(r.ID))
		if id == "" {
			continue
		}
		info := BeadInfo{
			ID:          id,
			Title:       r.Title,
			Description: r.Description,
			IssueType:   r.IssueType,
			Status:      r.Status,
			CloseReason: r.CloseReason,
			Labels:      r.Labels,
		}
		byID[id] = info

		// Extract target path from title, e.g. "Implement finally/frontend/polish.css"
		title := r.Title
		if strings.HasPrefix(title, "Implement ") {
			target := strings.TrimSpace(strings.TrimPrefix(title, "Implement "))
			target = filepath.ToSlash(filepath.Clean(target))
			byPath[target] = id
		}
	}

	return byID, byPath
}

// ParsePlanBeadMap extracts bead mappings from plan.md.
func ParsePlanBeadMap(planMD string) map[string]string {
	beadToPath := make(map[string]string)
	beadMapRE := regexp.MustCompile(`(?m)^###\s+([a-zA-Z0-9_-]+):\s*(\S+)`)
	for _, m := range beadMapRE.FindAllStringSubmatch(planMD, -1) {
		beadID := strings.TrimSpace(m[1])
		targetPath := filepath.ToSlash(filepath.Clean(strings.TrimSpace(m[2])))
		beadToPath[beadID] = targetPath
	}
	return beadToPath
}

func isFuturePhase(v orchestrator.WorkflowValidation, phaseID string) bool {
	if !v.HasPhasedDelivery() || phaseID == "" {
		return false
	}
	activeIdx := v.ActivePhaseIndex()
	if activeIdx < 0 {
		return false
	}
	for i, p := range v.DeliveryPhases {
		if strings.EqualFold(p.ID, phaseID) {
			return i > activeIdx
		}
	}
	return false
}

// GenerateRTM generates the full traceability matrix report for a rig.
func GenerateRTM(townRoot, rigName string) (*RTMReport, error) {
	rigDir := filepath.Join(townRoot, rigName, "mayor", "rig")
	if _, err := os.Stat(rigDir); err != nil {
		return nil, fmt.Errorf("rig directory not found: %s", rigDir)
	}

	report := &RTMReport{
		Rig:         rigName,
		GeneratedAt: time.Now().UTC(),
	}

	// 1. Load workflow profile if present
	prof, ok, _ := orchestrator.LoadRigWorkflowProfileFile(townRoot, rigName)
	if ok {
		report.ActivePhase = prof.ActivePhaseID()
	}

	// 2. Load beads from .beads/
	beadsByID, beadsByPath := LoadRigBeads(townRoot, rigName)

	// 3. Load plan.md if present
	planPath := filepath.Join(rigDir, "plan.md")
	var planBeadMap map[string]string
	if planData, err := os.ReadFile(planPath); err == nil {
		planBeadMap = ParsePlanBeadMap(string(planData))
	}

	// 4. Load TEST_PLAN.md
	testPlanPath := filepath.Join(rigDir, "TEST_PLAN.md")
	testPlanData, err := os.ReadFile(testPlanPath)
	if err != nil {
		return nil, fmt.Errorf("TEST_PLAN.md missing at %s — tester has not authored test plan yet", testPlanPath)
	}

	blocks := ParseTestPlanDetailed(string(testPlanData))
	if len(blocks) == 0 {
		return nil, fmt.Errorf("no requirement blocks found in %s", testPlanPath)
	}

	for _, b := range blocks {
		row := RTMRow{
			ReqID:       b.ReqID,
			Requirement: b.Requirement,
			Phase:       b.Phase,
			Level:       b.Level,
			BeadID:      b.BeadID,
			TestFile:    b.TestFile,
			Scenarios:   b.Scenarios,
			Assertions:  b.Assertions,
		}

		// Handle comma-separated test files if present (use primary test file for existence)
		primaryTestFile := b.TestFile
		if strings.Contains(primaryTestFile, ",") {
			parts := strings.Split(primaryTestFile, ",")
			primaryTestFile = strings.TrimSpace(parts[0])
		}
		if idx := strings.IndexAny(primaryTestFile, "([{"); idx >= 0 {
			primaryTestFile = strings.TrimSpace(primaryTestFile[:idx])
		}
		primaryTestFile = strings.TrimSpace(strings.Trim(primaryTestFile, "`*"))

		// Check test file existence on disk
		if primaryTestFile != "" {
			diskPath := filepath.Join(rigDir, primaryTestFile)
			if info, err := os.Stat(diskPath); err == nil {
				row.TestExists = true
				row.TestBytes = info.Size()
			}
		}

		// Resolve bead ID and status
		normBeadID := strings.TrimSpace(b.BeadID)
		if idx := strings.IndexAny(normBeadID, "([{"); idx >= 0 {
			normBeadID = strings.TrimSpace(normBeadID[:idx])
		}

		// If BeadID is placeholder or empty, attempt reverse lookup
		isPlaceholder := normBeadID == "" ||
			strings.EqualFold(normBeadID, "closed") ||
			strings.EqualFold(normBeadID, "done") ||
			strings.EqualFold(normBeadID, "n/a")

		if isPlaceholder {
			// Try lookup by primary test file
			cleanTestPath := filepath.ToSlash(filepath.Clean(primaryTestFile))
			if matchedID, found := beadsByPath[cleanTestPath]; found {
				normBeadID = matchedID
			} else {
				// Check plan.md mapping
				for bid, p := range planBeadMap {
					if p == cleanTestPath {
						normBeadID = bid
						break
					}
				}
			}
		}

		if info, exists := beadsByID[normBeadID]; exists {
			row.BeadID = normBeadID
			row.BeadStatus = info.Status
			row.BeadTitle = info.Title
		} else if isPlaceholder {
			row.BeadStatus = "closed"
			row.BeadID = "closed"
		} else {
			row.BeadID = normBeadID
			row.BeadStatus = "unknown"
		}

		// Audit checks
		if !row.TestExists && primaryTestFile != "" {
			if isFuturePhase(prof, row.Phase) {
				row.AuditStatus = AuditPending
				row.AuditMessage = "Future phase — scheduled for upcoming delivery phase"
				report.Pending++
			} else {
				row.AuditStatus = AuditFail
				row.AuditMessage = fmt.Sprintf("Planned test file missing on disk: %s", primaryTestFile)
				report.Failures++
			}
		} else if row.TestBytes > 0 && row.TestBytes < 50 {
			row.AuditStatus = AuditWarn
			row.AuditMessage = "Test file is very small or potentially a stub"
			report.Warnings++
		} else if row.BeadStatus == "in_progress" || row.BeadStatus == "open" {
			row.AuditStatus = AuditWarn
			row.AuditMessage = fmt.Sprintf("Bead %s still %s", row.BeadID, row.BeadStatus)
			report.Warnings++
		} else if row.TestExists {
			row.AuditStatus = AuditPass
			row.AuditMessage = "Test verified on disk"
			report.Passed++
		} else {
			row.AuditStatus = AuditPass
			report.Passed++
		}

		report.Rows = append(report.Rows, row)
	}

	report.TotalReqs = len(report.Rows)
	actionable := report.TotalReqs - report.Pending
	if actionable > 0 {
		report.ScorePct = float64(report.Passed) / float64(actionable) * 100.0
	} else if report.TotalReqs > 0 {
		report.ScorePct = float64(report.Passed) / float64(report.TotalReqs) * 100.0
	}

	// 5. Populate associated beads and categorize for business traceability
	seenBeads := make(map[string]bool)

	// Add beads directly referenced in the requirement rows
	for _, row := range report.Rows {
		beadID := row.BeadID
		if beadID == "" || strings.EqualFold(beadID, "closed") || strings.EqualFold(beadID, "pending") || strings.EqualFold(beadID, "unknown") {
			continue
		}
		if seenBeads[beadID] {
			continue
		}
		seenBeads[beadID] = true

		info, exists := beadsByID[beadID]
		desc := ""
		title := ""
		status := row.BeadStatus
		if exists {
			title = info.Title
			desc = cleanBeadDescription(info)
			if status == "" || status == "unknown" {
				status = info.Status
			}
		}

		reqLabel := CleanRequirementTitle(row.ReqID, row.Requirement)
		targetPath := row.TestFile
		if p, ok := planBeadMap[beadID]; ok {
			targetPath = p
		}

		cat, artifact, purpose := ClassifyBead(targetPath, rigName, true, reqLabel, title, desc)

		report.Beads = append(report.Beads, RTMBeadRow{
			ID:          beadID,
			Status:      status,
			Category:    cat,
			Artifact:    artifact,
			Requirement: reqLabel,
			Phase:       row.Phase,
			Title:       title,
			Description: desc,
			Purpose:     purpose,
		})
	}

	// Add other implementation beads for the rig (from beadsByID / plan.md)
	var remainingIDs []string
	for id, info := range beadsByID {
		if seenBeads[id] || isInternalPatrolBead(info) {
			continue
		}
		remainingIDs = append(remainingIDs, id)
	}
	sort.Strings(remainingIDs)

	for _, id := range remainingIDs {
		info := beadsByID[id]
		seenBeads[id] = true
		desc := cleanBeadDescription(info)

		targetPath := ""
		if p, ok := planBeadMap[id]; ok {
			targetPath = p
		} else {
			targetPath = desc
		}

		cat, artifact, purpose := ClassifyBead(targetPath, rigName, false, "", info.Title, desc)

		report.Beads = append(report.Beads, RTMBeadRow{
			ID:          id,
			Status:      info.Status,
			Category:    cat,
			Artifact:    artifact,
			Requirement: "-",
			Title:       info.Title,
			Description: desc,
			Purpose:     purpose,
		})
	}

	// Sort beads by category priority, category name, then artifact path
	sort.SliceStable(report.Beads, func(i, j int) bool {
		pi := categoryPriority(report.Beads[i].Category)
		pj := categoryPriority(report.Beads[j].Category)
		if pi != pj {
			return pi < pj
		}
		if report.Beads[i].Category != report.Beads[j].Category {
			return report.Beads[i].Category < report.Beads[j].Category
		}
		return report.Beads[i].Artifact < report.Beads[j].Artifact
	})

	// Compute delivery alignment summary counts
	for _, b := range report.Beads {
		report.Summary.TotalBeads++
		switch b.Category {
		case CategoryRequirement:
			report.Summary.Requirements++
		case CategoryComponentWeb, CategoryComponentData, CategoryComponentUI, CategoryComponentCore:
			report.Summary.Components++
		case CategoryInfrastructure:
			report.Summary.Infrastructure++
		case CategoryBuild:
			report.Summary.Build++
		case CategoryDocsConfig:
			report.Summary.DocsConfig++
		case CategoryTesting:
			report.Summary.Testing++
		}
	}

	return report, nil
}

// CleanRequirementTitle extracts a concise, business-readable title from a requirement statement.
func CleanRequirementTitle(reqID, rawRequirement string) string {
	raw := strings.TrimSpace(rawRequirement)
	if raw == "" {
		return reqID
	}
	// 1. Colon delimiter (e.g. "Domain validation: ...", "Project REST API: ...")
	if idx := strings.Index(raw, ":"); idx > 0 && idx <= 40 {
		return strings.TrimSpace(raw[:idx])
	}
	// 2. Dash delimiter (e.g. "Domain validation - ...")
	if idx := strings.Index(raw, " - "); idx > 0 && idx <= 40 {
		return strings.TrimSpace(raw[:idx])
	}
	// 3. Parenthesis delimiter (e.g. "Task REST API (list, create...)")
	if idx := strings.Index(raw, "("); idx > 0 && idx <= 35 {
		candidate := strings.TrimSpace(raw[:idx])
		if len(candidate) >= 4 {
			return candidate
		}
	}
	// 4. Common verb cuts: " runs ", " provides ", " implements ", " asserts ", " verifies "
	for _, verb := range []string{" runs ", " provides ", " implements ", " asserts ", " verifies ", " manages "} {
		if idx := strings.Index(strings.ToLower(raw), verb); idx > 0 && idx <= 35 {
			candidate := strings.TrimSpace(raw[:idx])
			if len(candidate) >= 4 {
				return candidate
			}
		}
	}
	// 5. Punctuation cut (comma or semicolon)
	if idx := strings.IndexAny(raw, ",;"); idx > 0 && idx <= 30 {
		candidate := strings.TrimSpace(raw[:idx])
		if len(candidate) >= 4 {
			return candidate
		}
	}
	// 6. Word boundary cut
	if len(raw) > 28 {
		cut := raw[:28]
		if lastSpace := strings.LastIndex(cut, " "); lastSpace > 10 {
			return cut[:lastSpace]
		}
		return cut
	}
	return raw
}

// CleanArtifactPath cleans the target artifact path by stripping rig prefixes and leading dot-slashes.
func CleanArtifactPath(path, rig string) string {
	cleaned := strings.TrimSpace(path)
	cleaned = strings.TrimPrefix(cleaned, "./")
	if rig != "" {
		cleaned = strings.TrimPrefix(cleaned, rig+"/")
		cleaned = strings.TrimPrefix(cleaned, strings.ToLower(rig)+"/")
	}
	// Also strip common subproject folders like taskboard/ or finally/ if present
	for _, pfx := range []string{"taskboard/", "finally/"} {
		if strings.HasPrefix(strings.ToLower(cleaned), pfx) {
			cleaned = cleaned[len(pfx):]
			break
		}
	}
	return cleaned
}

func categoryPriority(cat string) int {
	switch cat {
	case CategoryRequirement:
		return 1
	case CategoryComponentWeb, CategoryComponentData, CategoryComponentUI, CategoryComponentCore:
		return 2
	case CategoryTesting:
		return 3
	case CategoryInfrastructure:
		return 4
	case CategoryBuild:
		return 5
	case CategoryDocsConfig:
		return 6
	default:
		return 7
	}
}

// ClassifyBead categorizes a delivery artifact and derives a human-readable capability summary.
func ClassifyBead(path string, rig string, isReq bool, reqName string, title, desc string) (category, artifact, purpose string) {
	artifact = CleanArtifactPath(path, rig)
	lowerPath := strings.ToLower(artifact)
	base := strings.ToLower(filepath.Base(lowerPath))

	if isReq {
		category = CategoryRequirement
		if reqName != "" && !strings.HasPrefix(strings.ToLower(reqName), "phase-") {
			purpose = reqName
		} else {
			purpose = cleanPurposeFromTitleOrDesc(title, desc, artifact)
		}
		return
	}

	// 1. Infrastructure & DevOps
	if strings.HasPrefix(lowerPath, "deploy/") || strings.HasPrefix(lowerPath, "k8s/") ||
		strings.Contains(lowerPath, "/deploy/") || strings.Contains(lowerPath, "/k8s/") ||
		strings.Contains(base, "dockerfile") || strings.Contains(base, "dockerignore") ||
		strings.Contains(base, "docker-compose") || strings.HasSuffix(base, ".yaml") || strings.HasSuffix(base, ".yml") {
		category = CategoryInfrastructure
		switch {
		case strings.Contains(base, "dockerignore"):
			purpose = "Docker build context exclusion rules"
		case strings.Contains(base, "dockerfile"):
			purpose = "Container image build specification"
		case strings.Contains(base, "deployment"):
			purpose = "Kubernetes Deployment manifest"
		case strings.Contains(base, "service"):
			purpose = "Kubernetes Service specification"
		case strings.Contains(base, "secret"):
			purpose = "Kubernetes Secrets configuration template"
		case strings.Contains(base, "ingress"):
			purpose = "Kubernetes Ingress routing specification"
		case strings.Contains(base, "compose"):
			purpose = "Docker Compose multi-service definition"
		default:
			purpose = "Deployment manifest & configuration"
		}
		return
	}

	// 2. Build & Tooling
	if base == "go.mod" || base == "go.sum" || base == "package.json" || base == "package-lock.json" ||
		base == "yarn.lock" || base == "pnpm-lock.yaml" || strings.HasPrefix(base, "tsconfig") ||
		strings.HasPrefix(base, "tailwind") || strings.HasPrefix(base, "postcss") ||
		strings.HasPrefix(base, "next.config") || strings.HasPrefix(base, "vite.config") ||
		base == "pyproject.toml" || base == "uv.lock" || base == "requirements.txt" ||
		base == "makefile" || strings.HasSuffix(base, ".sh") || strings.HasSuffix(base, ".ps1") {
		category = CategoryBuild
		switch {
		case base == "go.mod":
			purpose = "Go module definition & dependencies"
		case base == "go.sum":
			purpose = "Go dependency checksums"
		case base == "package.json":
			purpose = "Node.js dependencies & npm scripts"
		case base == "package-lock.json" || base == "yarn.lock" || base == "pnpm-lock.yaml":
			purpose = "Node.js resolved dependency lockfile"
		case strings.HasPrefix(base, "tsconfig"):
			purpose = "TypeScript compiler configuration"
		case strings.HasPrefix(base, "tailwind"):
			purpose = "Tailwind CSS styling configuration"
		case strings.HasPrefix(base, "postcss"):
			purpose = "PostCSS processor plugin configuration"
		case base == "pyproject.toml":
			purpose = "Python packaging & dependencies"
		case base == "uv.lock":
			purpose = "Python package lockfile"
		case strings.HasSuffix(base, ".sh") || strings.HasSuffix(base, ".ps1"):
			purpose = "Startup & execution control script"
		default:
			purpose = "Build & dependency configuration"
		}
		return
	}

	// 3. Testing
	if strings.Contains(base, "playwright.config") || strings.HasSuffix(base, "_test.go") ||
		strings.HasSuffix(base, ".spec.ts") || strings.HasSuffix(base, "_test.py") ||
		strings.HasPrefix(lowerPath, "test/") || strings.Contains(lowerPath, "/tests/") {
		category = CategoryTesting
		switch {
		case strings.Contains(base, "playwright.config"):
			purpose = "Playwright end-to-end runner configuration"
		case strings.Contains(base, "api_test"):
			purpose = "REST API integration test suite"
		case strings.Contains(base, "ui_content_test"):
			purpose = "UI markup & asset test suite"
		case strings.Contains(base, "store_test") || strings.Contains(base, "postgres_integration_test"):
			purpose = "Database storage contract test suite"
		default:
			purpose = "Automated test suite"
		}
		return
	}

	// 4. Docs & Config
	if strings.HasPrefix(base, "readme") || base == ".gitignore" || base == ".gitkeep" ||
		strings.HasPrefix(base, ".env") || strings.HasPrefix(base, "architecture") ||
		strings.HasPrefix(base, "spec") || strings.HasPrefix(base, "plan") {
		category = CategoryDocsConfig
		switch {
		case strings.HasPrefix(base, "readme"):
			purpose = "Project documentation & setup instructions"
		case base == ".gitignore":
			purpose = "Git version control ignore rules"
		case base == ".gitkeep":
			purpose = "Directory structure placeholder"
		case strings.HasPrefix(base, ".env"):
			purpose = "Environment variables template"
		default:
			purpose = "Project documentation & configuration"
		}
		return
	}

	// 5. Application Components
	switch {
	case strings.Contains(lowerPath, "/templates/") || strings.HasSuffix(base, ".html"):
		category = CategoryComponentUI
		purpose = "Web UI templates & HTML markup"
	case strings.Contains(lowerPath, "/static/") || strings.HasSuffix(base, ".js") || strings.HasSuffix(base, ".tsx") || strings.HasSuffix(base, ".jsx"):
		category = CategoryComponentUI
		if strings.HasSuffix(base, ".css") {
			purpose = "Application stylesheet & visual layout"
		} else {
			purpose = "Interactive client-side JavaScript"
		}
	case strings.HasSuffix(base, ".css"):
		category = CategoryComponentUI
		purpose = "Application stylesheet & visual layout"
	case base == "assets.go":
		category = CategoryComponentUI
		purpose = "Embedded static assets filesystem loader"
	case base == "app.go" || strings.Contains(lowerPath, "/web/"):
		category = CategoryComponentWeb
		if base == "api.go" {
			purpose = "HTTP REST API handlers & responses"
		} else {
			purpose = "Web application router & HTTP middleware"
		}
	case strings.Contains(lowerPath, "/store/") || strings.Contains(lowerPath, "/db/"):
		category = CategoryComponentData
		switch base {
		case "sqlite.go":
			purpose = "SQLite database storage adapter"
		case "postgres.go":
			purpose = "PostgreSQL database storage adapter"
		case "store.go":
			purpose = "Data store interfaces & factory"
		default:
			purpose = "Data persistence layer"
		}
	case base == "main.go" || strings.HasPrefix(lowerPath, "cmd/"):
		category = CategoryComponentCore
		purpose = "Application entrypoint & CLI startup"
	case strings.Contains(lowerPath, "/config/"):
		category = CategoryComponentCore
		purpose = "Runtime configuration loader & validation"
	case strings.Contains(lowerPath, "/model/"):
		category = CategoryComponentCore
		purpose = "Core domain models & entity structures"
	default:
		category = CategoryComponentCore
		purpose = cleanPurposeFromTitleOrDesc(title, desc, artifact)
	}
	return
}

func cleanPurposeFromTitleOrDesc(title, desc, artifact string) string {
	candidate := strings.TrimSpace(desc)
	if candidate == "" || (strings.HasPrefix(candidate, "Implement ") && strings.HasSuffix(candidate, " per architecture")) {
		candidate = strings.TrimSpace(title)
	}
	// Strip standard boilerplate prefixes/suffixes
	candidate = strings.TrimPrefix(candidate, "Implement ")
	if idx := strings.Index(candidate, " per architecture"); idx > 0 {
		candidate = candidate[:idx]
	}
	if idx := strings.Index(candidate, ": see architecture.md"); idx > 0 {
		candidate = candidate[:idx]
	}
	candidate = strings.TrimSpace(candidate)
	// If candidate is just the file path, provide a generic clean label
	if candidate == artifact || strings.HasSuffix(artifact, candidate) {
		base := filepath.Base(artifact)
		return fmt.Sprintf("Implementation for %s", base)
	}
	if candidate != "" {
		return candidate
	}
	return fmt.Sprintf("Implementation for %s", filepath.Base(artifact))
}

func cleanBeadDescription(info BeadInfo) string {
	desc := strings.TrimSpace(info.Description)
	title := strings.TrimSpace(info.Title)
	if desc != "" && !strings.EqualFold(desc, title) {
		if strings.HasSuffix(desc, ": see architecture.md") && strings.HasSuffix(title, "per architecture") {
			return title
		}
		lines := strings.Split(desc, "\n")
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" {
				return l
			}
		}
	}
	if title != "" {
		return title
	}
	return desc
}

func isInternalPatrolBead(info BeadInfo) bool {
	if info.IssueType == "event" {
		return true
	}
	lower := strings.ToLower(info.Title)
	if strings.HasSuffix(lower, " patrol") || strings.HasPrefix(lower, "patrol") {
		return true
	}
	if strings.HasPrefix(lower, "mol-") || strings.HasPrefix(lower, "wisp-") {
		return true
	}
	for _, role := range []string{"analyst for", "architect for", "planner for", "qa for", "refinery for", "witness for", "deacon for"} {
		if strings.Contains(lower, role) {
			return true
		}
	}
	if strings.Contains(info.ID, "-rig-") {
		return true
	}
	return false
}

// Markdown formats the RTMReport as a clean GitHub-flavored markdown report.
func (r *RTMReport) Markdown() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Requirements Traceability Matrix — %s\n\n", r.Rig))
	if r.ActivePhase != "" {
		sb.WriteString(fmt.Sprintf("**Active Phase**: `%s`  \n", r.ActivePhase))
	}
	sb.WriteString(fmt.Sprintf("**Generated**: %s  \n", r.GeneratedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("**Coverage Score**: %.1f%% (%d/%d verified, %d warnings, %d failures)\n\n",
		r.ScorePct, r.Passed, r.TotalReqs, r.Warnings, r.Failures))

	sb.WriteString("## Requirements Verification Matrix\n\n")
	sb.WriteString("| Requirement ID | Intent / Description | Phase | Bead | Test File | Level | Audit |\n")
	sb.WriteString("| :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")

	for _, row := range r.Rows {
		reqDesc := CleanRequirementTitle(row.ReqID, row.Requirement)
		reqDesc = strings.ReplaceAll(reqDesc, "|", "\\|")

		beadStr := row.BeadID
		if row.BeadStatus != "" && row.BeadStatus != "closed" {
			beadStr = fmt.Sprintf("%s (%s)", row.BeadID, row.BeadStatus)
		}

		auditIcon := "✓ PASS"
		if row.AuditStatus == AuditWarn {
			auditIcon = "⚠ WARN"
		} else if row.AuditStatus == AuditPending {
			auditIcon = "⏳ PEND"
		} else if row.AuditStatus == AuditFail {
			auditIcon = "✗ FAIL"
		}

		sb.WriteString(fmt.Sprintf("| `%s` | %s | %s | %s | `%s` | %s | %s |\n",
			row.ReqID, reqDesc, row.Phase, beadStr, row.TestFile, row.Level, auditIcon))
	}

	if r.Failures > 0 || r.Warnings > 0 {
		sb.WriteString("\n## Audit Findings\n\n")
		for _, row := range r.Rows {
			if row.AuditStatus != AuditPass {
				sb.WriteString(fmt.Sprintf("- **[%s] %s**: %s\n", row.AuditStatus, row.ReqID, row.AuditMessage))
			}
		}
	}

	if r.Summary.TotalBeads > 0 {
		sb.WriteString("\n## Delivery Inventory Summary\n\n")
		sb.WriteString(fmt.Sprintf("- **Total Tracked Beads**: %d\n", r.Summary.TotalBeads))
		sb.WriteString(fmt.Sprintf("- **Requirements Delivered**: %d verified business capabilities\n", r.Summary.Requirements))
		if r.Summary.Components > 0 {
			sb.WriteString(fmt.Sprintf("- **Supporting Components**: %d application & UI modules\n", r.Summary.Components))
		}
		if r.Summary.Infrastructure > 0 {
			sb.WriteString(fmt.Sprintf("- **Infrastructure & DevOps**: %d container & deployment manifests\n", r.Summary.Infrastructure))
		}
		if r.Summary.Build > 0 {
			sb.WriteString(fmt.Sprintf("- **Build & Tooling**: %d package & build definitions\n", r.Summary.Build))
		}
		if r.Summary.DocsConfig > 0 {
			sb.WriteString(fmt.Sprintf("- **Docs & Config**: %d repository configurations\n", r.Summary.DocsConfig))
		}
		if r.Summary.Testing > 0 {
			sb.WriteString(fmt.Sprintf("- **Testing**: %d test runner configurations\n", r.Summary.Testing))
		}
	}

	if len(r.Beads) > 0 {
		sb.WriteString("\n## Implementation & Component Alignment\n\n")
		sb.WriteString("| Bead | Status | Category | Target Artifact | Capability / Purpose |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
		for _, b := range r.Beads {
			purpose := b.Purpose
			if purpose == "" {
				purpose = b.Description
				if purpose == "" {
					purpose = b.Title
				}
			}
			purpose = strings.ReplaceAll(purpose, "|", "\\|")
			artifact := b.Artifact
			if artifact == "" {
				artifact = b.Requirement
			}
			artifact = strings.ReplaceAll(artifact, "|", "\\|")

			sb.WriteString(fmt.Sprintf("| `%s` | %s | %s | `%s` | %s |\n", b.ID, b.Status, b.Category, artifact, purpose))
		}
	}

	return sb.String()
}

// RenderTerminal renders a beautiful styled terminal table using internal/style.
func (r *RTMReport) RenderTerminal(summaryOnly ...bool) string {
	onlySummary := len(summaryOnly) > 0 && summaryOnly[0]
	var sb strings.Builder

	header := fmt.Sprintf("Requirements Traceability Matrix: %s", style.Bold.Render(r.Rig))
	if r.ActivePhase != "" {
		header += fmt.Sprintf(" (Phase: %s)", style.Info.Render(r.ActivePhase))
	}
	sb.WriteString(header + "\n\n")

	tbl := style.NewTable(
		style.Column{Name: "Requirement", Width: 26},
		style.Column{Name: "Bead", Width: 12},
		style.Column{Name: "Status", Width: 10},
		style.Column{Name: "Test File", Width: 36},
		style.Column{Name: "Audit", Width: 8},
	)

	for _, row := range r.Rows {
		auditStr := style.Success.Render("✓ PASS")
		if row.AuditStatus == AuditWarn {
			auditStr = style.Warning.Render("⚠ WARN")
		} else if row.AuditStatus == AuditPending {
			auditStr = style.Dim.Render("⏳ PEND")
		} else if row.AuditStatus == AuditFail {
			auditStr = style.Error.Render("✗ FAIL")
		}

		req := CleanRequirementTitle(row.ReqID, row.Requirement)
		if len(req) > 24 {
			req = req[:21] + "..."
		}

		testFile := CleanArtifactPath(row.TestFile, r.Rig)
		if len(testFile) > 34 {
			testFile = "..." + testFile[len(testFile)-31:]
		}

		status := row.BeadStatus
		if status == "" {
			status = "closed"
		}

		tbl.AddRow(req, row.BeadID, status, testFile, auditStr)
	}

	sb.WriteString(tbl.Render())
	sb.WriteString("\n\n")

	pendingStr := ""
	if r.Pending > 0 {
		pendingStr = fmt.Sprintf(", %d pending", r.Pending)
	}
	scoreMsg := fmt.Sprintf("Coverage: %.1f%% (%d/%d verified%s, %d warnings, %d failures)",
		r.ScorePct, r.Passed, r.TotalReqs, pendingStr, r.Warnings, r.Failures)
	if r.Failures == 0 {
		sb.WriteString(style.Success.Render(ui.IconPass+" ") + style.Bold.Render(scoreMsg) + "\n")
	} else {
		sb.WriteString(style.Error.Render(ui.IconFail+" ") + style.Bold.Render(scoreMsg) + "\n")
	}

	if r.Failures > 0 || r.Warnings > 0 {
		sb.WriteString("\n" + style.Bold.Render("Audit Details:") + "\n")
		for _, row := range r.Rows {
			if row.AuditStatus != AuditPass {
				badge := style.Warning.Render("[WARN]")
				if row.AuditStatus == AuditFail {
					badge = style.Error.Render("[FAIL]")
				}
				sb.WriteString(fmt.Sprintf("  %s %s: %s\n", badge, style.Bold.Render(row.ReqID), row.AuditMessage))
			}
		}
	}

	if r.Summary.TotalBeads > 0 {
		sb.WriteString("\n" + style.Bold.Render("Delivery Alignment:") + fmt.Sprintf(" %d Tracked Beads\n", r.Summary.TotalBeads))
		sb.WriteString(fmt.Sprintf("  • %-18s %d verified business capabilities\n", style.Info.Render("Requirements:"), r.Summary.Requirements))
		if r.Summary.Components > 0 {
			sb.WriteString(fmt.Sprintf("  • %-18s %d application & UI modules\n", style.Info.Render("Components:"), r.Summary.Components))
		}
		if r.Summary.Infrastructure > 0 {
			sb.WriteString(fmt.Sprintf("  • %-18s %d container & deployment manifests\n", style.Info.Render("Infrastructure:"), r.Summary.Infrastructure))
		}
		if r.Summary.Build > 0 {
			sb.WriteString(fmt.Sprintf("  • %-18s %d package & build definitions\n", style.Info.Render("Build & Tooling:"), r.Summary.Build))
		}
		if r.Summary.DocsConfig > 0 {
			sb.WriteString(fmt.Sprintf("  • %-18s %d repository configurations & docs\n", style.Info.Render("Docs & Config:"), r.Summary.DocsConfig))
		}
		if r.Summary.Testing > 0 {
			sb.WriteString(fmt.Sprintf("  • %-18s %d test runner configurations\n", style.Info.Render("Testing:"), r.Summary.Testing))
		}
	}

	if !onlySummary && len(r.Beads) > 0 {
		sb.WriteString("\n" + style.Bold.Render("Implementation & Component Alignment:") + "\n\n")

		beadTbl := style.NewTable(
			style.Column{Name: "Bead", Width: 10},
			style.Column{Name: "Status", Width: 8},
			style.Column{Name: "Category", Width: 18},
			style.Column{Name: "Target Artifact", Width: 32},
			style.Column{Name: "Capability / Purpose", Width: 42},
		)

		for _, b := range r.Beads {
			statusStr := style.Dim.Render(b.Status)
			if b.Status == "open" || b.Status == "in_progress" {
				statusStr = style.Warning.Render(b.Status)
			} else if b.Status == "closed" {
				statusStr = style.Success.Render("closed")
			}

			catStr := b.Category
			if b.Category == CategoryRequirement {
				catStr = style.Bold.Render(b.Category)
			} else if strings.HasPrefix(b.Category, "Component") {
				catStr = style.Info.Render(b.Category)
			} else if b.Category == CategoryInfrastructure {
				catStr = style.Warning.Render(b.Category)
			}

			artifact := b.Artifact
			if artifact == "" {
				artifact = b.Requirement
			}
			if len(artifact) > 30 {
				artifact = "..." + artifact[len(artifact)-27:]
			}

			purpose := b.Purpose
			if purpose == "" {
				purpose = b.Description
				if purpose == "" {
					purpose = b.Title
				}
			}
			if len(purpose) > 40 {
				purpose = purpose[:37] + "..."
			}

			beadTbl.AddRow(b.ID, statusStr, catStr, artifact, purpose)
		}

		sb.WriteString(beadTbl.Render())
		sb.WriteString("\n")
	}

	return sb.String()
}
