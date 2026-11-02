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
	selectors := extractE2ESelectors(rigDir, v.LayoutRoot, archDoc)
	if len(selectors) > 0 {
		b.WriteString("\n**DOM selectors / IDs to use in tests (do not invent others):**\n")
		for _, s := range selectors {
			b.WriteString("- `" + s + "`\n")
		}
	}

	if IsE2ETestPath(beadPath) {
		if srcBlock := formatExistingAppFilesForE2E(rigDir, v.LayoutRoot); srcBlock != "" {
			b.WriteString("\n" + srcBlock + "\n")
		}
	}

	// Suggested verify commands.
	b.WriteString("\n**Rules:**\n")
	b.WriteString("- The app/dev-server must be running on the documented URL/port before tests execute.\n")
	b.WriteString("- Match exact button labels, `#id` selectors, text changes, and API routes (e.g. `/ping` vs `/api/ping`) from the existing app files shown above.\n")
	b.WriteString("- Use only selectors listed above or explicitly present in the existing frontend files.\n")
	b.WriteString("- Verify command must match the `Verify:` line from plan.md for this bead.\n")

	return strings.TrimSpace(b.String())
}

func formatExistingAppFilesForE2E(rigDir, layoutRoot string) string {
	candidates := []string{
		"web/index.html", "index.html", "static/index.html", "public/index.html",
		"web/app.js", "app.js", "static/app.js", "src/App.tsx", "src/App.jsx",
		"cmd/server/main.go", "main.go", "app.py", "main.py",
	}
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	seen := map[string]bool{}
	var b strings.Builder
	for _, rel := range candidates {
		var paths []string
		if layout != "" && layout != "." {
			paths = append(paths, filepath.Join(rigDir, filepath.FromSlash(layout+"/"+rel)))
		}
		paths = append(paths, filepath.Join(rigDir, filepath.FromSlash(rel)))
		for _, p := range paths {
			if seen[p] {
				continue
			}
			data, err := os.ReadFile(p)
			if err != nil || len(data) == 0 {
				continue
			}
			seen[p] = true
			relLabel, rerr := filepath.Rel(rigDir, p)
			if rerr != nil {
				relLabel = rel
			}
			relLabel = filepath.ToSlash(relLabel)
			snippet := strings.TrimSpace(string(data))
			if len(snippet) > 2000 {
				snippet = snippet[:2000] + "\n...(truncated)"
			}
			if b.Len() == 0 {
				b.WriteString("**Existing application files on disk (assert against these exact selectors, text, and routes):**\n")
			}
			b.WriteString("\n`" + relLabel + "`:\n```\n" + snippet + "\n```\n")
			break
		}
	}
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

func extractE2ESelectors(rigDir, layoutRoot, archDoc string) []string {
	seen := map[string]bool{}
	var ids []string
	layout := strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")

	// Parse existing index.html / app.js for IDs.
	for _, rel := range []string{"index.html", "web/index.html", "app.js", "web/app.js", "src/App.tsx", "src/App.jsx"} {
		var paths []string
		if layout != "" && layout != "." {
			paths = append(paths, filepath.Join(rigDir, filepath.FromSlash(layout+"/"+rel)))
		}
		paths = append(paths, filepath.Join(rigDir, filepath.FromSlash(rel)))
		for _, p := range paths {
			data, err := os.ReadFile(p)
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
