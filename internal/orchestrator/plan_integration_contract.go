package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var archOwnershipPathRE = regexp.MustCompile("`(linkshelf/[^`]+)`")

// IntegrationContractScopeNote tells plan_review/planning when ## Integration contract is required
// for the active delivery phase (not the full profile union).
func (v WorkflowValidation) IntegrationContractScopeNote() string {
	v = v.ForActivePhase()
	if profileHasServerEntrypoint(v) {
		mainPath := ""
		for _, f := range v.RequiredFiles {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if strings.Contains(f, "/cmd/") && strings.HasSuffix(f, "main.go") {
				mainPath = f
				break
			}
		}
		if mainPath != "" {
			return fmt.Sprintf("**Integration contract (this phase):** required — active phase includes `%s`. `plan.md` must have **## Integration contract** with SPEC HTTP routes, dependency order, and exported symbols.", mainPath)
		}
		return "**Integration contract (this phase):** required — active phase includes a server entrypoint. `plan.md` must have **## Integration contract**."
	}
	if !v.HasPhasedDelivery() {
		return ""
	}
	return "**Integration contract (this phase):** **not required** — active phase `required_files` has no `cmd/.../main.go`. Do **not** fail plan review for a missing ## Integration contract; that section belongs in the phase that implements the server entrypoint."
}

// renderPlanIntegrationContract builds ## Integration contract from SPEC/architecture when
// the profile includes a server entrypoint. Used by planning sync so implementation is not
// blocked waiting for the planner LLM to paste this section.
func renderPlanIntegrationContract(rigDir string, v WorkflowValidation) string {
	if !profileHasServerEntrypoint(v) {
		return ""
	}
	v = v.ForActivePhase()
	specDoc := readRigDoc(rigDir, "SPEC.md")
	archDoc := readRigDoc(rigDir, "architecture.md")
	if specDoc == "" && archDoc == "" {
		return ""
	}
	mainPath := ""
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if strings.Contains(f, "/cmd/") && strings.HasSuffix(f, "main.go") {
			mainPath = f
			break
		}
	}
	if mainPath == "" {
		mainPath = v.LayoutRootDir() + "/cmd/server/main.go"
	}

	var b strings.Builder
	b.WriteString("## Integration contract\n\n")
	b.WriteString(fmt.Sprintf("**Entrypoint:** `%s`\n\n", mainPath))
	b.WriteString("**Startup / dependency order (SPEC + architecture):**\n\n")
	for _, line := range extractNumberedLines(specDoc+"\n"+archDoc, 8) {
		b.WriteString("- ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	if b.Len() == len("## Integration contract\n\n**Entrypoint:** `"+mainPath+"`\n\n**Startup / dependency order (SPEC + architecture):**\n\n") {
		b.WriteString("- Open DB, run `InitSchema`, assign `store.DB`, register handlers on `http.DefaultServeMux`, listen on `:8080`.\n")
	}

	routes := parseSpecHTTPRouteTable(specDoc)
	if len(routes) == 0 {
		api := parseAPISmokeSpecText(specDoc, v)
		for _, p := range api.Probes {
			if p.Path == "" {
				continue
			}
			routes = append(routes, specHTTPRouteRow{
				Method: strings.ToUpper(strings.TrimSpace(p.Method)),
				Path:   p.Path,
			})
		}
	}
	if len(routes) > 0 {
		b.WriteString("\n**HTTP routes (SPEC verbatim):**\n\n")
		b.WriteString("| Method | Path |\n|--------|------|\n")
		for _, row := range routes {
			b.WriteString("| ")
			b.WriteString(row.Method)
			b.WriteString(" | `")
			b.WriteString(row.Path)
			b.WriteString("` |\n")
		}
	}

	if rows := extractArchitectureOwnershipRows(archDoc); len(rows) > 0 {
		b.WriteString("\n**Per-file exports (from architecture.md):**\n\n")
		for _, row := range rows {
			b.WriteString("- ")
			b.WriteString(row)
			b.WriteString("\n")
		}
	} else {
		b.WriteString("\n**Per-file exports:** see `architecture.md` per-file ownership table; use only names documented there.\n")
	}
	b.WriteString("\n")
	return b.String()
}

func extractNumberedLines(doc string, max int) []string {
	var out []string
	for _, line := range strings.Split(doc, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 4 || line[0] < '1' || line[0] > '9' {
			continue
		}
		i := 0
		for i < len(line) && (line[i] >= '0' && line[i] <= '9' || line[i] == '.') {
			i++
		}
		if i < len(line) && (line[i] == '.' || line[i] == ')') {
			i++
		}
		rest := strings.TrimSpace(line[i:])
		if rest != "" {
			out = append(out, rest)
		}
		if len(out) >= max {
			break
		}
	}
	return out
}

func extractArchitectureOwnershipRows(archDoc string) []string {
	if archDoc == "" {
		return nil
	}
	var out []string
	lines := strings.Split(archDoc, "\n")
	inTable := false
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(lower, "per-file ownership") || strings.Contains(lower, "file | owns") {
			inTable = true
			continue
		}
		if inTable && strings.HasPrefix(strings.TrimSpace(line), "## ") && !strings.Contains(lower, "ownership") {
			break
		}
		if !inTable {
			continue
		}
		if !strings.Contains(line, "linkshelf/") && !strings.Contains(line, "`") {
			continue
		}
		if m := archOwnershipPathRE.FindStringSubmatch(line); len(m) >= 2 {
			out = append(out, strings.TrimSpace(m[1]))
		}
	}
	return out
}

func splicePlanIntegrationContract(planDoc, block string) string {
	planDoc = strings.TrimSpace(planDoc)
	block = strings.TrimSpace(block)
	if block == "" {
		return planDoc
	}
	if planDoc == "" {
		return "# Implementation plan\n\n" + block
	}
	if head, tail, ok := splitPlanIntegrationContractSection(planDoc); ok {
		var b strings.Builder
		b.WriteString(strings.TrimRight(head, "\n"))
		b.WriteString("\n\n")
		b.WriteString(block)
		if tail != "" {
			b.WriteString("\n")
			b.WriteString(tail)
		}
		return b.String()
	}
	if strings.HasPrefix(planDoc, "# Implementation plan") {
		if idx := strings.Index(planDoc, "\n## "); idx >= 0 {
			return planDoc[:idx] + "\n\n" + block + strings.TrimPrefix(planDoc[idx:], "\n")
		}
		return planDoc + "\n\n" + block
	}
	return "# Implementation plan\n\n" + block + "\n\n" + planDoc
}

func splitPlanIntegrationContractSection(planDoc string) (head, tail string, ok bool) {
	lines := strings.Split(planDoc, "\n")
	start := -1
	for i, line := range lines {
		if integrationContractHeadingRE.MatchString(line) {
			start = i
			break
		}
	}
	if start < 0 {
		return "", "", false
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		trim := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trim, "## ") && !integrationContractHeadingRE.MatchString(trim) {
			end = i
			break
		}
	}
	head = strings.Join(lines[:start], "\n")
	if end < len(lines) {
		tail = strings.Join(lines[end:], "\n")
	}
	return head, tail, true
}

// ensurePlanIntegrationContract patches plan.md when the section is missing or has stale HTTP paths.
func ensurePlanIntegrationContract(rigDir string, v WorkflowValidation) (bool, error) {
	if !profileHasServerEntrypoint(v) {
		return false, nil
	}
	path := filepath.Join(rigDir, "plan.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	planDoc := string(data)
	specDoc := readRigDoc(rigDir, "SPEC.md")
	existing := ExtractSpecMarkdownSection(planDoc, "Integration contract")
	if existing != "" && len(checkPlanIntegrationContract(planDoc, specDoc, v)) == 0 {
		return false, nil
	}
	block := renderPlanIntegrationContract(rigDir, v)
	if block == "" {
		return false, nil
	}
	body := splicePlanIntegrationContract(planDoc, block)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0644); err != nil {
		return false, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, err
	}
	return true, nil
}
