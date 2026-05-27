package orchestrator

import (
	"fmt"
	"os"
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

// ValidateHTTPContract checks HTML refs and handler source against architecture static routing.
func ValidateHTTPContract(townRoot, rig string, v WorkflowValidation) error {
	if !WorkflowNeedsRuntimeSmoke(townRoot, rig, v) {
		return nil
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	contract := HTTPContractFromRig(townRoot, rig, v)
	var issues []string
	issues = append(issues, validateWebHTMLContract(rigDir, v, contract.Static)...)
	if path := firstHandlerImplementPath(v); path != "" {
		body, err := os.ReadFile(filepath.Join(rigDir, filepath.FromSlash(path)))
		if err == nil {
			issues = append(issues, handlerContractIssues(townRoot, rig, string(body), v)...)
		}
	}
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(issues, "; "))
}

func firstHandlerImplementPath(v WorkflowValidation) string {
	for _, rel := range append(append([]string(nil), v.RequiredFiles...), v.UnionRequiredFiles()...) {
		if IsHTTPHandlerImplementPath(rel) {
			return filepath.ToSlash(strings.TrimSpace(rel))
		}
	}
	return ""
}

func validateWebHTMLContract(rigDir string, v WorkflowValidation, mapping WebStaticMapping) []string {
	var issues []string
	for _, htmlRel := range webHTMLPaths(v) {
		abs := filepath.Join(rigDir, filepath.FromSlash(htmlRel))
		body, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		webRoot := webRootDir(rigDir, htmlRel, v)
		for _, m := range htmlAttrRefContractRE.FindAllStringSubmatch(string(body), -1) {
			if len(m) < 3 {
				continue
			}
			ref := strings.TrimSpace(m[2])
			if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "http") || strings.HasPrefix(ref, "/api/") {
				continue
			}
			lower := strings.ToLower(ref)
			if !strings.HasSuffix(lower, ".js") && !strings.HasSuffix(lower, ".css") {
				continue
			}
			if hint := mapping.StaticRefMismatchHint(ref); hint != "" {
				issues = append(issues, fmt.Sprintf("%s: %s", htmlRel, hint))
				continue
			}
			disk := mapping.WebDiskPathForURLRef(webRoot, htmlRel, ref)
			if disk == "" {
				continue
			}
			if info, err := os.Stat(disk); err != nil || info.IsDir() {
				issues = append(issues, fmt.Sprintf("%s references %q but %s is missing under web/", htmlRel, ref, filepath.Base(disk)))
			}
		}
	}
	return issues
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
	return nil
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
