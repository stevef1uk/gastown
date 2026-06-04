package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var archOwnershipPathRE = regexp.MustCompile("`(linkshelf/[^`]+)`")

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

	api := parseAPISmokeSpecText(specDoc+"\n"+archDoc, v)
	if len(api.Probes) > 0 {
		b.WriteString("\n**HTTP routes (SPEC verbatim):**\n\n")
		b.WriteString("| Method | Path |\n|--------|------|\n")
		seen := map[string]bool{}
		for _, p := range api.Probes {
			if p.Source == "static" && p.Path == "" {
				continue
			}
			path := normalizeSmokePath(p.Path)
			if path == "" {
				continue
			}
			key := strings.ToUpper(strings.TrimSpace(p.Method)) + " " + path
			if seen[key] {
				continue
			}
			seen[key] = true
			b.WriteString("| ")
			b.WriteString(strings.ToUpper(strings.TrimSpace(p.Method)))
			b.WriteString(" | `")
			b.WriteString(path)
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

// ensurePlanIntegrationContract patches plan.md when the section is missing (idempotent).
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
	if ExtractSpecMarkdownSection(planDoc, "Integration contract") != "" {
		return false, nil
	}
	block := renderPlanIntegrationContract(rigDir, v)
	if block == "" {
		return false, nil
	}
	var out strings.Builder
	if strings.HasPrefix(planDoc, "# Implementation plan") {
		if idx := strings.Index(planDoc, "\n## "); idx >= 0 {
			out.WriteString(planDoc[:idx])
			out.WriteString("\n\n")
			out.WriteString(block)
			out.WriteString(strings.TrimPrefix(planDoc[idx:], "\n"))
		} else {
			out.WriteString(planDoc)
			out.WriteString("\n\n")
			out.WriteString(block)
		}
	} else {
		out.WriteString("# Implementation plan\n\n")
		out.WriteString(block)
		out.WriteString(planDoc)
	}
	body := out.String()
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
