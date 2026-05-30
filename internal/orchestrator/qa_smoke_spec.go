package orchestrator

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// APISmokeSpec is derived from rig SPEC/architecture/plan (not hardcoded routes).
type APISmokeSpec struct {
	Port   int
	Probes []HTTPEndpointProbe // canonical: one curl per documented endpoint
	// Derived from Probes (kept for plan alignment and legacy callers).
	GETPaths          []string
	GETEmptyJSONArray []string
	POSTProbes        []POSTSmokeProbe
	StaticAssets      []string
	// ResetPaths are removed under the smoke workDir before starting the server (doc-driven).
	ResetPaths []string
	// ServerStart is the background server shell fragment (e.g. go run ./cmd/server).
	ServerStart string
}

// POSTSmokeProbe is a POST endpoint plus minimal JSON body for QA smoke.
type POSTSmokeProbe struct {
	Path string
	Body string
}

var (
	apiTableRowRE = regexp.MustCompile(`(?im)^\|\s*(GET|POST|PUT|DELETE|PATCH)\s*\|\s*([^|]+)\|\s*([^|]*)`)
	apiBacktickPathRE = regexp.MustCompile(`(?i)\b(GET|POST|PUT|DELETE|PATCH)\s+/([a-zA-Z0-9_./{}-]+)`)
	htmlAssetRefRE  = regexp.MustCompile(`(?i)\b(src|href)\s*=\s*["']([^"'#][^"']*)["']`)
)

// LoadAPISmokeSpecFromRig reads SPEC.md, architecture.md, and plan.md under mayor/rig.
func LoadAPISmokeSpecFromRig(townRoot, rig string, v WorkflowValidation) (APISmokeSpec, error) {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var merged string
	for _, name := range []string{"SPEC.md", "architecture.md", "plan.md"} {
		data, err := os.ReadFile(filepath.Join(rigDir, name))
		if err != nil {
			continue
		}
		merged += "\n" + string(data)
	}
	if strings.TrimSpace(merged) == "" {
		return APISmokeSpec{Port: devServerPortFromText("", v)}, nil
	}
	spec := parseAPISmokeSpecText(merged, v)
	mapping := LoadWebStaticMappingFromRig(townRoot, rig, v)
	appendStaticProbes(&spec, staticAssetsFromRig(rigDir, v, mapping))
	syncAPISmokeSpecDerivedFields(&spec)
	enrichAPISmokeSpec(&spec, merged, v)
	return spec, nil
}

func parseAPISmokeSpecText(text string, v WorkflowValidation) APISmokeSpec {
	spec := APISmokeSpec{Port: devServerPortFromText(text, v)}
	seen := map[string]bool{}
	record := func(method, path, detail string) {
		appendAPIProbe(&spec, seen, method, path, detail, text)
	}
	for _, m := range apiTableRowRE.FindAllStringSubmatch(text, -1) {
		if len(m) >= 3 {
			detail := m[0]
			if len(m) >= 4 {
				detail = m[3]
			}
			record(m[1], strings.Trim(m[2], " `'\""), detail)
		}
	}
	for _, m := range apiBacktickPathRE.FindAllStringSubmatch(text, -1) {
		if len(m) >= 3 {
			record(m[1], "/"+strings.Trim(m[2], "/"), text)
		}
	}
	syncAPISmokeSpecDerivedFields(&spec)
	return spec
}

func normalizeSmokePath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "`\"'")
	if path == "" {
		return ""
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	// Drop path params for smoke (use collection path only).
	if i := strings.Index(path, "{"); i > 0 {
		path = strings.TrimSuffix(path[:i], "/")
		if path == "" {
			return ""
		}
	}
	path = strings.TrimRight(path, ".,;:!?)")
	return path
}

func defaultPOSTBodyFromSpec(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, `"title"`) && strings.Contains(lower, `"url"`):
		return `{"title":"qa-smoke","url":"https://example.com/qa"}`
	case strings.Contains(lower, "bookmark"):
		return `{"title":"qa-smoke","url":"https://example.com/qa"}`
	default:
		return `{"title":"qa-smoke","url":"https://example.com/qa"}`
	}
}

func devServerPortFromText(text string, v WorkflowValidation) int {
	for _, src := range []string{text, v.QAVerifyCommand, v.ActivePhaseQAVerifyCommand()} {
		for _, m := range localhostPortRE.FindAllStringSubmatch(src, -1) {
			if len(m) >= 2 {
				if port, err := parsePort(m[1]); err == nil {
					return port
				}
			}
		}
	}
	return 8080
}

func parsePort(s string) (int, error) {
	var port int
	_, err := fmt.Sscanf(s, "%d", &port)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return port, nil
}

func staticAssetsFromRig(rigDir string, v WorkflowValidation, mapping WebStaticMapping) []string {
	seen := map[string]bool{}
	var out []string
	for _, htmlRel := range webHTMLPaths(v) {
		abs := filepath.Join(rigDir, filepath.FromSlash(htmlRel))
		body, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		for _, m := range htmlAssetRefRE.FindAllStringSubmatch(string(body), -1) {
			if len(m) < 3 {
				continue
			}
			ref := strings.TrimSpace(m[2])
			if ref == "" || strings.HasPrefix(ref, "#") {
				continue
			}
			url := mapping.SmokeURLForHTMLRef(ref)
			if url == "" || seen[url] {
				continue
			}
			seen[url] = true
			out = append(out, url)
		}
	}
	sort.Strings(out)
	return out
}

func webHTMLPaths(v WorkflowValidation) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range append(append([]string(nil), v.RequiredFiles...), v.UnionRequiredFiles()...) {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" || seen[f] || !strings.HasSuffix(strings.ToLower(f), ".html") || !strings.Contains(f, "/web/") {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

func webRootDir(rigDir, htmlRel string, v WorkflowValidation) string {
	parts := strings.Split(filepath.ToSlash(htmlRel), "/web/")
	if len(parts) > 1 {
		return filepath.Join(rigDir, filepath.FromSlash(parts[0]), "web")
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" || layout == "." {
		return filepath.Join(rigDir, "web")
	}
	return filepath.Join(rigDir, filepath.FromSlash(layout), "web")
}

// SmokeShellStrictPrefix enables fail-fast behavior when the probe runs under bash -c.
const SmokeShellStrictPrefix = "set -euo pipefail; "

// BuildRuntimeSmokeShell returns bash that starts the app server then runs doc-derived curl probes.
// Each HTTPEndpointProbe becomes one curl (same routes as architecture contract validation).
func BuildRuntimeSmokeShell(workDir string, spec APISmokeSpec) string {
	workDir = strings.Trim(strings.TrimSpace(workDir), `"'`)
	if workDir == "" {
		return ""
	}
	serverStart := strings.TrimSpace(spec.ServerStart)
	if serverStart == "" {
		return ""
	}
	port := spec.Port
	if port == 0 {
		port = 8080
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	var parts []string
	parts = append(parts, "cd "+bashSingleQuote(workDir))
	parts = append(parts, smokeResetShellParts(spec.ResetPaths)...)
	parts = append(parts, "rm -f .gt-smoke.pid")
	parts = append(parts, "("+serverStart+" >/dev/null 2>&1 & echo $! >.gt-smoke.pid)")
	if m := smokeStepMarker("wait_root"); m != "" {
		parts = append(parts, m)
	}
	parts = append(parts, fmt.Sprintf(`_gtok=0; for _i in 1 2 3 4 5; do curl -sf --connect-timeout 1 --max-time 2 %s/ >/dev/null && _gtok=1 && break; sleep 1; done`, base))
	parts = append(parts, `test "$_gtok" = 1`)
	for _, probe := range spec.orderedSmokeProbes() {
		parts = append(parts, probe.shellSteps(base)...)
	}
	parts = append(parts, `(_gtsrv=$(cat .gt-smoke.pid 2>/dev/null); kill ${_gtsrv} 2>/dev/null || true; rm -f .gt-smoke.pid)`)
	if len(spec.orderedSmokeProbes()) == 0 {
		return ""
	}
	return SmokeShellStrictPrefix + strings.Join(parts, " && ")
}

// IsProfileDerivedSmokeCommand reports whether cmd is gt-agent's doc-derived curl smoke script.
func IsProfileDerivedSmokeCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, ".gt-smoke.pid") &&
		strings.Contains(lower, strings.ToLower(smokeStepMarkerPrefix))
}
