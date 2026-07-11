package orchestrator

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// HTTPContract summarizes static routing from rig architecture docs.
type HTTPContract struct {
	Static WebStaticMapping
}

// HTTPContractFromRig loads static URL rules from architecture/SPEC/plan.
func HTTPContractFromRig(townRoot, rig string, v WorkflowValidation) HTTPContract {
	return HTTPContract{Static: LoadWebStaticMappingFromRig(townRoot, rig, v)}
}

// IsHTTPContractRelevantPath reports paths whose writes can affect HTTP routing.
// Only HTML files and handler source (.go) trigger contract validation — CSS/JS
// files are the referenced content, not routing definitions, and don't need re-validation.
func IsHTTPContractRelevantPath(relPath string) bool {
	rel := filepath.ToSlash(strings.TrimSpace(relPath))
	if strings.Contains(rel, "/web/") {
		lower := strings.ToLower(rel)
		if strings.HasSuffix(lower, ".html") {
			return true
		}
	}
	return IsHTTPHandlerImplementPath(rel)
}

// IsHTTPHandlerImplementPath is production handler source (not tests).
func IsHTTPHandlerImplementPath(relPath string) bool {
	rel := filepath.ToSlash(strings.TrimSpace(relPath))
	return strings.Contains(rel, "/api/handlers") && strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go")
}

// IsHTTPHandlerTestPath is handler package tests.
func IsHTTPHandlerTestPath(relPath string) bool {
	rel := filepath.ToSlash(strings.TrimSpace(relPath))
	return strings.Contains(rel, "/api/handlers") && strings.HasSuffix(rel, "_test.go")
}

// IsHTTPRoutingGuidanceBead includes handler, handler tests, and web assets.
func IsHTTPRoutingGuidanceBead(relPath string) bool {
	return IsHTTPContractRelevantPath(relPath) || IsHTTPHandlerTestPath(relPath)
}

var htmlAttrRefContractRE = regexp.MustCompile(`(?i)\b(src|href)\s*=\s*["']([^"'#][^"']*)["']`)

func firstHandlerImplementPath(v WorkflowValidation) string {
	for _, rel := range append(append([]string(nil), v.RequiredFiles...), v.UnionRequiredFiles()...) {
		if IsHTTPHandlerImplementPath(rel) {
			return filepath.ToSlash(strings.TrimSpace(rel))
		}
	}
	return ""
}

func handlerContractIssues(townRoot, rig, body string, v WorkflowValidation) []string {
	mapping := LoadWebStaticMappingFromRig(townRoot, rig, v)
	var issues []string
	lower := strings.ToLower(body)
	if strings.Contains(body, "os.Chdir") {
		issues = append(issues, "handlers.go must not use os.Chdir — use a fixed webRoot (e.g. filepath.Join(moduleDir, \"web\"))")
	}
	webDir := LoadHTTPImplementationProfile(townRoot, rig, v).WebDiskDir
	if webDir == "" {
		webDir = "web"
	}
	if strings.Contains(lower, "index.html") && !strings.Contains(lower, "/"+webDir+"/") && !strings.Contains(lower, `"`+webDir+`"`) {
		issues = append(issues, "serve index from "+webDir+"/index.html per architecture, not CWD index.html")
	}
	if mapping.StaticURLPrefix != "" && strings.Contains(lower, "static/") && !strings.Contains(lower, "/"+webDir+"/") && !strings.Contains(lower, `"`+webDir+`"`) {
		issues = append(issues, "static files should be read from "+webDir+"/ on disk with URL prefix "+mapping.StaticURLPrefix)
	}
	issues = append(issues, HandlerStaticServePatternIssues(townRoot, rig, body, v)...)
	return issues
}

// ValidateImplementWrittenContent rejects polecat patterns that break HTTP contract (GT-VERIFY-001)
// and cross-bead symbol duplication in shared Go packages.
func ValidateImplementWrittenContent(townRoot, rig, mayorRigDir, relPath, content string, v WorkflowValidation) error {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if relPath == "" {
		return nil
	}
	if IsHTTPHandlerTestPath(relPath) {
		for _, issue := range HandlerTestMissingModuleChdirIssues(townRoot, rig, relPath, content, v) {
			return fmt.Errorf("%s: %s", relPath, issue)
		}
	}
	if IsHTTPHandlerImplementPath(relPath) && strings.Contains(content, "os.Chdir") {
		return fmt.Errorf("%s must not use os.Chdir — use a fixed webRoot (e.g. filepath.Join(moduleDir, \"web\")); table cases should hit architecture paths (GET /, static assets, .. traversal)", relPath)
	}
	if IsHTTPHandlerImplementPath(relPath) {
		for _, issue := range HandlerStaticServePatternIssues(townRoot, rig, content, v) {
			return fmt.Errorf("%s: %s", relPath, issue)
		}
	}
	if err := ValidateImplementCrossBeadContent(mayorRigDir, relPath, content, v); err != nil {
		return err
	}
	if IsCmdMainImplementPath(relPath) {
		if err := validateServerEntrypointWiring(relPath, content); err != nil {
			return err
		}
	}
	if IsMainDockerfile(relPath, v.LayoutRoot) {
		archDoc := readRigDoc(mayorRigDir, "architecture.md")
		if err := ValidateDockerfileAgainstArchitecture(content, archDoc, relPath); err != nil {
			return err
		}
	}
	return nil
}

// IsMainDockerfile reports whether relPath is the project's primary Dockerfile
// (not a test/ subdirectory image like Playwright).
func IsMainDockerfile(relPath, layoutRoot string) bool {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if !strings.HasSuffix(strings.ToLower(relPath), "dockerfile") {
		return false
	}
	if strings.Contains(relPath, "/test/") || strings.HasPrefix(relPath, "test/") {
		return false
	}
	base := filepath.Base(relPath)
	if strings.ToLower(base) != "dockerfile" {
		return false
	}
	dir := filepath.ToSlash(filepath.Dir(relPath))
	layoutRoot = strings.Trim(filepath.ToSlash(strings.TrimSpace(layoutRoot)), "/")
	return dir == "." || dir == "" || dir == layoutRoot
}

func validateServerEntrypointWiring(relPath, content string) error {
	hasServer := strings.Contains(content, "ListenAndServe")
	if !hasServer {
		return nil
	}
	if !strings.Contains(content, "HandleFunc") && !strings.Contains(content, "Handle(") {
		return fmt.Errorf("%s starts a server but registers no routes — add http.HandleFunc calls per SPEC HTTP table", relPath)
	}
	if !hasNonStdlibImport(content) {
		return fmt.Errorf("%s only imports standard library — import local handler/store packages to register API routes", relPath)
	}
	return nil
}

var stdlibPrefixes = []string{
	"archive/", "bufio", "builtin", "bytes", "cmp", "compress/", "container/",
	"context", "crypto/", "database/", "debug/", "embed", "encoding/", "errors",
	"expvar", "flag", "fmt", "go/", "hash/", "html/", "image/", "index/",
	"io", "log", "maps", "math/", "mime", "net/", "os", "path/", "plugin",
	"reflect", "regexp", "runtime/", "slices", "sort", "strconv", "strings",
	"structs", "sync", "syscall", "testing", "text/", "time", "unicode",
	"unique", "unsafe",
}

var goImportRE = regexp.MustCompile(`^\s*"[^"]+"\s*$`)

func hasNonStdlibImport(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if !goImportRE.MatchString(t) {
			continue
		}
		imp := strings.Trim(t, `"`)
		isStd := false
		for _, p := range stdlibPrefixes {
			if strings.HasPrefix(imp, p) || imp == p || imp == p[:len(p)-1] {
				isStd = true
				break
			}
		}
		if !isStd && !strings.Contains(imp, "_test") {
			return true
		}
	}
	return false
}

// FormatHTTPRoutingGuidanceForBead returns implement-context notes for handler/web beads.
func FormatHTTPRoutingGuidanceForBead(townRoot, rig, beadPath string, v WorkflowValidation) string {
	if !IsHTTPRoutingGuidanceBead(beadPath) {
		return ""
	}
	prof := LoadHTTPImplementationProfile(townRoot, rig, v)
	if block := prof.FormatHTTPImplementGuidance(beadPath, v); block != "" {
		return block
	}
	mapping := LoadWebStaticMappingFromRig(townRoot, rig, v)
	var b strings.Builder
	b.WriteString("### HTTP routing / tests (architecture)\n")
	if mapping.StaticURLPrefix != "" {
		b.WriteString(fmt.Sprintf("- Static URLs use prefix **%s** (see architecture HTTP table).\n", mapping.StaticURLPrefix))
	} else {
		b.WriteString("- Match static paths in `web/index.html` to the architecture HTTP table.\n")
	}
	return strings.TrimSpace(b.String())
}
