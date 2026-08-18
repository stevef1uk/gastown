package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// TestContentReport describes whether a rig's automated tests (Playwright,
// pytest, Go) assert real application content rather than only HTTP status or
// element visibility.
type TestContentReport struct {
	Issues []string
}

func (r TestContentReport) IsClean() bool {
	return len(r.Issues) == 0
}

// DetectWeakTestAssertions scans a rig for test files (Playwright specs, pytest
// files, Go tests) and flags suites whose assertions can pass while the app
// serves a placeholder/fallback page or returns empty/constant bodies.
//
// It is intentionally stack-agnostic: node/Playwright, Python/pytest, and Go
// tests are all recognized. It also flags tests that actively assert the
// placeholder text (e.g. `assert "Finally" in dashboard.text`), which enshrines
// the fallback as correct behavior.
func DetectWeakTestAssertions(mayorRigDir string) TestContentReport {
	report := TestContentReport{}

	for _, path := range findTestFiles(mayorRigDir) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if !strings.Contains(content, "test(") && !strings.Contains(content, "describe(") &&
			!strings.Contains(content, "def test_") && !strings.Contains(content, "func Test") {
			continue
		}

		rel, _ := filepath.Rel(mayorRigDir, path)
		relSlash := filepath.ToSlash(rel)

		if placeholder := enshrinedPlaceholderText(content); placeholder != "" {
			report.Issues = append(report.Issues,
				"test file `"+relSlash+"` asserts the placeholder/fallback text `"+placeholder+
					"` (e.g. `assert \"Finally\" in dashboard.text` or `getByText(\"Finally\")`). "+
					"The fallback page is NOT the application — remove the placeholder and assert "+
					"real rendered content instead (page heading, watchlist tickers, dollar amounts).")
			continue
		}

		if !testAssertsContent(content) {
			report.Issues = append(report.Issues,
				"test file `"+relSlash+"` does not assert real content. "+
					"Tests that only check status codes (`status_code == 200`, `status().toBe(200)`), "+
					"`content-type`, or generic `toBeVisible()` on the body pass even when the app "+
					"serves a placeholder page or empty/constant responses. Assert concrete content: "+
					"for Playwright use `getByText`/`getByRole`/`toHaveText`; for pytest assert on "+
					"`response.json()`/`.text` values (e.g. `assert data[\"ticker\"] == \"AAPL\"`); "+
					"for Go use `httptest` and inspect `recorder.Body` / decoded JSON.")
		}
	}

	return report
}

// enshrinedPlaceholderText returns placeholder text a test asserts as if it were
// the real app, or "" when none is found.
func enshrinedPlaceholderText(content string) string {
	for _, p := range []string{"Finally", "Coming soon", "Under construction", "Placeholder", "Welcome!"} {
		if strings.Contains(content, `"`+p+`"`) || strings.Contains(content, `'`+p+`'`) ||
			strings.Contains(content, "`"+p+"`") {
			return p
		}
	}
	return ""
}

var pyContentAssertRE = regexp.MustCompile(
	`(?i)\.json\(\)|\.text\b|in (?:response|resp|dashboard|html|page)\.text|` +
		`assert (?:response|resp|dashboard|html|page)\.text`)
var goContentAssertRE = regexp.MustCompile(
	`(?i)\.Body\b|strings\.Contains|json\.Unmarshal|\.Decode\(&|got := |want := `)
var e2eContentAssertRE = regexp.MustCompile(
	`(?i)getByText|getByRole|getByLabel|getByPlaceholder|toHaveText|toContainText|` +
		`toHaveValue|toHaveTitle|textContent|innerText|toHaveAttribute`)
var e2eTestIDRE = regexp.MustCompile(`(?i)getByTestId\s*\(\s*['"][A-Za-z0-9_-]+['"]\s*\)`)

// testAssertsContent reports whether a test file's body contains at least one
// assertion on concrete rendered/returned content, across the supported stacks.
func testAssertsContent(content string) bool {
	stripped := stripLineComments(content)

	// A spec that drives a real browser page must assert DOM content, not just
	// API JSON or page visibility. Strong API assertions don't rescue a weak
	// page-render test.
	if strings.Contains(stripped, "page.goto") || strings.Contains(stripped, "page.locator") {
		if !hasDOMContentMatcher(stripped) {
			return false
		}
	}

	if e2eContentAssertRE.MatchString(stripped) || e2eTestIDRE.MatchString(stripped) {
		return true
	}
	if pyContentAssertRE.MatchString(stripped) {
		return true
	}
	if goContentAssertRE.MatchString(stripped) {
		return true
	}

	// Playwright visibility checks on a concrete selector (not body/page) count.
	for _, m := range e2eExpectToBeVisibleRE.FindAllStringIndex(stripped, -1) {
		expr := expectArgBetween(stripped, m[0], m[1])
		if expr == "" {
			continue
		}
		if e2eGenericLocatorRE.MatchString(expr) {
			continue
		}
		return true
	}
	return false
}

// hasDOMContentMatcher reports whether the text uses a Playwright matcher that
// asserts rendered DOM content (text, role, label, attribute, value, title).
func hasDOMContentMatcher(content string) bool {
	if e2eContentAssertRE.MatchString(content) {
		return true
	}
	if e2eTestIDRE.MatchString(content) {
		return true
	}
	return false
}

// e2eExpectToBeVisibleRE locates `expect(...)` spans.
var e2eExpectToBeVisibleRE = regexp.MustCompile(`(?i)expect\s*\(`)

// e2eGenericLocatorRE matches visibility checks on the bare page/document body
// or on a locator whose selector targets the generic page shell.
var e2eGenericLocatorRE = regexp.MustCompile(
	`(?i)\b(?:page|document|window)\b(?:\.|\)|\s)` +
		`|locator\s*\(\s*['"](?:body|html|#__next|#root|body\s*>?\s*\*?)['"]\s*\)` +
		`|['"](?:body|html)['"]`)

// expectArgBetween returns the text between the expect(...) opening paren and the
// matching close paren that precedes `.toBeVisible`, using paren balance.
func expectArgBetween(s string, start, _ int) string {
	open := strings.Index(s[start:], "(")
	if open < 0 {
		return ""
	}
	i := start + open + 1
	depth := 1
	j := i
	for ; j < len(s); j++ {
		switch s[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				rest := s[j+1:]
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rest)), ".tobevisible") {
					return strings.TrimSpace(s[i:j])
				}
				return ""
			}
		}
	}
	return ""
}

func stripLineComments(content string) string {
	var b strings.Builder
	inString := byte(0)
	for i := 0; i < len(content); i++ {
		c := content[i]
		if inString != 0 {
			b.WriteByte(c)
			if c == '\\' && i+1 < len(content) {
				i++
				b.WriteByte(content[i])
				continue
			}
			if c == inString {
				inString = 0
			}
			continue
		}
		if c == '"' || c == '\'' || c == '`' {
			inString = c
			b.WriteByte(c)
			continue
		}
		if c == '/' && i+1 < len(content) && content[i+1] == '/' {
			for i < len(content) && content[i] != '\n' {
				i++
			}
			if i < len(content) {
				b.WriteByte('\n')
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// findTestFiles locates test files across stacks: Playwright specs
// (*.spec.ts/js/tsx, *.e2e.ts), pytest files (test_*.py, *_test.py under tests/),
// and Go test files (*_test.go).
func findTestFiles(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		lower := strings.ToLower(filepath.ToSlash(path))
		if strings.Contains(lower, "node_modules") || strings.Contains(lower, ".venv") ||
			strings.Contains(lower, "/.git/") {
			return nil
		}
		base := strings.ToLower(d.Name())

		if strings.HasSuffix(base, ".spec.ts") || strings.HasSuffix(base, ".spec.js") ||
			strings.HasSuffix(base, ".spec.tsx") || strings.HasSuffix(base, ".e2e.ts") ||
			strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".test.js") {
			if IsE2ETestPath(lower) {
				out = append(out, path)
			}
			return nil
		}
		if strings.HasSuffix(base, "_test.go") {
			out = append(out, path)
			return nil
		}
		if (strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")) &&
			strings.HasSuffix(base, ".py") {
			out = append(out, path)
			return nil
		}
		return nil
	})
	return out
}

// FormatTestContentGuidance returns QA guidance from a TestContentReport.
func FormatTestContentGuidance(report TestContentReport) string {
	if report.IsClean() {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Tests must assert real app content\n")
	b.WriteString("Some tests pass while the app renders a placeholder or returns empty/constant bodies. Strengthen them:\n\n")
	for _, issue := range report.Issues {
		b.WriteString("- " + issue + "\n")
	}
	return strings.TrimSpace(b.String())
}