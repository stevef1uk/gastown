package rtm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// RTMReport holds the complete Requirements Traceability Matrix for a rig.
type RTMReport struct {
	Rig         string    `json:"rig"`
	GeneratedAt time.Time `json:"generated_at"`
	ActivePhase string    `json:"active_phase,omitempty"`
	Rows        []RTMRow  `json:"rows"`
	TotalReqs   int       `json:"total_requirements"`
	Passed      int       `json:"passed"`
	Warnings    int       `json:"warnings"`
	Pending     int       `json:"pending"`
	Failures    int       `json:"failures"`
	ScorePct    float64   `json:"score_percent"`
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

	if cur != nil {
		blocks = append(blocks, *cur)
	}

	return blocks
}

// BeadInfo holds metadata about an existing bead in the rig database.
type BeadInfo struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
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
	cmd.Env = append(os.Environ(), "BEADS_DIR="+beadsDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return byID, byPath
	}
	out = beads.StripStdoutWarnings(out)

	var rows []struct {
		ID          string   `json:"id"`
		Title       string   `json:"title"`
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

	return report, nil
}

// Markdown formats the RTMReport as a clean GitHub-flavored markdown table.
func (r *RTMReport) Markdown() string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# Requirements Traceability Matrix — %s\n\n", r.Rig))
	if r.ActivePhase != "" {
		sb.WriteString(fmt.Sprintf("**Active Phase**: `%s`  \n", r.ActivePhase))
	}
	sb.WriteString(fmt.Sprintf("**Generated**: %s  \n", r.GeneratedAt.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("**Coverage Score**: %.1f%% (%d/%d verified, %d warnings, %d failures)\n\n",
		r.ScorePct, r.Passed, r.TotalReqs, r.Warnings, r.Failures))

	sb.WriteString("| Requirement ID | Intent / Description | Phase | Bead | Test File | Level | Audit |\n")
	sb.WriteString("| :--- | :--- | :--- | :--- | :--- | :--- | :--- |\n")

	for _, row := range r.Rows {
		reqDesc := row.Requirement
		if len(reqDesc) > 80 {
			reqDesc = reqDesc[:77] + "..."
		}
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

	return sb.String()
}

// RenderTerminal renders a beautiful styled terminal table using internal/style.
func (r *RTMReport) RenderTerminal() string {
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

		req := row.ReqID
		if len(req) > 24 {
			req = req[:21] + "..."
		}

		testFile := row.TestFile
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

	return sb.String()
}
