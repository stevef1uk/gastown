package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FormatE2ETestBeadChecklist returns concrete implementation requirements for e2e / Playwright
// / docker-compose test beads. It extracts selectors, URLs, and test commands from SPEC.md and
// architecture.md so the polecat does not invent them.
func FormatE2ETestBeadChecklist(rigDir, beadPath string, v WorkflowValidation) string {
	beadPath = filepath.ToSlash(strings.TrimSpace(beadPath))
	if !IsE2ETestPath(beadPath) && !strings.Contains(strings.ToLower(beadPath), "docker-compose") {
		return ""
	}

	specData, err := os.ReadFile(filepath.Join(rigDir, "SPEC.md"))
	if err != nil {
		specData = nil
	}
	archData, err := os.ReadFile(filepath.Join(rigDir, "architecture.md"))
	if err != nil {
		archData = nil
	}
	specDoc := ""
	if specData != nil {
		specDoc = string(specData)
	}
	archDoc := ""
	if archData != nil {
		archDoc = string(archData)
	}

	var b strings.Builder
	b.WriteString("### E2E / integration test checklist\n")

	// URLs / ports the app is expected to serve on.
	urls := extractE2EAppURLs(specDoc, archDoc)
	if len(urls) > 0 {
		b.WriteString("**App URLs referenced in SPEC/architecture:**\n")
		for _, u := range urls {
			b.WriteString("- `" + u + "`\n")
		}
	}

	// DOM selectors referenced in existing frontend files or docs.
	selectors := extractE2ESelectors(rigDir, archDoc)
	if len(selectors) > 0 {
		b.WriteString("\n**DOM selectors / IDs to use in tests (do not invent others):**\n")
		for _, s := range selectors {
			b.WriteString("- `" + s + "`\n")
		}
	}

	// Suggested verify commands.
	b.WriteString("\n**Rules:**\n")
	b.WriteString("- The app/dev-server must be running on the documented URL/port before tests execute.\n")
	b.WriteString("- If using docker-compose, the `app`/`web` service must build and run the real app — not `sleep infinity`.\n")
	b.WriteString("- Use only selectors listed above or explicitly documented in architecture.md / SPEC.md.\n")
	b.WriteString("- Verify command must match the `Verify:` line from plan.md for this bead.\n")

	return strings.TrimSpace(b.String())
}

var e2eURLRE = regexp.MustCompile(`(?i)(https?://localhost:\d+|http://127\.0\.0\.1:\d+|localhost:\d+)`)

func extractE2EAppURLs(specDoc, archDoc string) []string {
	seen := map[string]bool{}
	var out []string
	for _, doc := range []string{specDoc, archDoc} {
		for _, m := range e2eURLRE.FindAllString(doc, -1) {
			u := strings.TrimSpace(m)
			if u != "" && !seen[u] {
				seen[u] = true
				out = append(out, u)
			}
		}
	}
	return out
}

func extractE2ESelectors(rigDir, archDoc string) []string {
	seen := map[string]bool{}
	var ids []string

	// Parse existing index.html / app.js for IDs.
	for _, rel := range []string{"index.html", "web/index.html", "app.js", "web/app.js", "src/App.tsx", "src/App.jsx"} {
		data, err := os.ReadFile(filepath.Join(rigDir, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		for _, m := range e2eIDRE.FindAllStringSubmatch(string(data), -1) {
			if len(m) >= 2 {
				id := strings.TrimSpace(m[1])
				if id != "" && !seen[id] {
					seen[id] = true
					ids = append(ids, "#"+id)
				}
			}
		}
	}

	// Parse architecture.md for documented selectors.
	for _, m := range e2eSelectorRE.FindAllStringSubmatch(archDoc, -1) {
		if len(m) >= 2 {
			sel := strings.TrimSpace(m[1])
			if sel != "" && !seen[sel] {
				seen[sel] = true
				ids = append(ids, sel)
			}
		}
	}

	return ids
}

var e2eIDRE = regexp.MustCompile(`(?:id|for)=['"]([^'"]+)['"]`)
var e2eSelectorRE = regexp.MustCompile(`(?i)(?:selector|element|id)\s*[:=]\s*['"]?([#.]?[\w-]+)['"]?`)
