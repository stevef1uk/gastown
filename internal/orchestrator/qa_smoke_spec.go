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
	Port              int
	GETPaths          []string // paths to probe with curl -sf
	GETEmptyJSONArray []string // subset of GETPaths that must return literal []
	POSTProbes        []POSTSmokeProbe
	StaticAssets      []string // root-relative paths e.g. /app.js
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
	spec.StaticAssets = staticAssetsFromRig(rigDir, v)
	return spec, nil
}

func parseAPISmokeSpecText(text string, v WorkflowValidation) APISmokeSpec {
	spec := APISmokeSpec{Port: devServerPortFromText(text, v)}
	getSeen := map[string]bool{}
	postSeen := map[string]bool{}

	record := func(method, path, detail string) {
		path = normalizeSmokePath(path)
		if path == "" || !strings.HasPrefix(path, "/") {
			return
		}
		method = strings.ToUpper(strings.TrimSpace(method))
		detailLower := strings.ToLower(detail)
		switch method {
		case "GET":
			if getSeen[path] {
				return
			}
			getSeen[path] = true
			spec.GETPaths = append(spec.GETPaths, path)
			if strings.Contains(detailLower, "json array") || strings.Contains(detailLower, "`[]`") ||
				strings.Contains(detailLower, "returns `[]`") || strings.Contains(detailLower, "not `null`") ||
				strings.Contains(detailLower, "[]") && strings.Contains(detailLower, "empty") {
				spec.GETEmptyJSONArray = append(spec.GETEmptyJSONArray, path)
			}
		case "POST":
			if postSeen[path] || strings.Contains(path, "{") {
				return
			}
			postSeen[path] = true
			spec.POSTProbes = append(spec.POSTProbes, POSTSmokeProbe{
				Path: path,
				Body: defaultPOSTBodyFromSpec(text),
			})
		}
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
			record(m[1], "/"+strings.Trim(m[2], "/"), "")
		}
	}
	sort.Strings(spec.GETPaths)
	sort.Strings(spec.GETEmptyJSONArray)
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

func staticAssetsFromRig(rigDir string, v WorkflowValidation) []string {
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
			if ref == "" || strings.HasPrefix(ref, "#") || strings.HasPrefix(ref, "http") || strings.HasPrefix(ref, "/api/") {
				continue
			}
			if !strings.HasSuffix(strings.ToLower(ref), ".js") && !strings.HasSuffix(strings.ToLower(ref), ".css") {
				continue
			}
			if !strings.HasPrefix(ref, "/") {
				ref = "/" + strings.TrimPrefix(filepath.ToSlash(ref), "/")
			}
			if seen[ref] {
				continue
			}
			seen[ref] = true
			out = append(out, ref)
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

// BuildRuntimeSmokeShell returns a bash go run + curl probe from profile/docs.
// Steps after the background server are joined with && so a failed GET / cannot be
// masked by a later passing API curl (GT-VERIFY-003).
func BuildRuntimeSmokeShell(workDir string, spec APISmokeSpec) string {
	workDir = strings.Trim(strings.TrimSpace(workDir), `"'`)
	if workDir == "" {
		return ""
	}
	port := spec.Port
	if port == 0 {
		port = 8080
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	var parts []string
	parts = append(parts, "cd "+workDir)
	parts = append(parts, "rm -f .gt-smoke.pid")
	parts = append(parts, "(go run ./cmd/server >/dev/null 2>&1 & echo $! >.gt-smoke.pid)")
	parts = append(parts, fmt.Sprintf(`_gtok=0; for _i in 1 2 3 4 5; do curl -sf --connect-timeout 1 --max-time 2 %s/ >/dev/null && _gtok=1 && break; sleep 1; done`, base))
	parts = append(parts, `test "$_gtok" = 1`)
	for _, asset := range spec.StaticAssets {
		parts = append(parts, fmt.Sprintf(`curl -sf --connect-timeout 1 --max-time 2 %s%s >/dev/null`, base, asset))
	}
	emptySet := map[string]bool{}
	for _, p := range spec.GETEmptyJSONArray {
		emptySet[p] = true
	}
	for _, path := range spec.GETPaths {
		if path == "/" {
			continue
		}
		if emptySet[path] {
			parts = append(parts, fmt.Sprintf(`test "$(curl -s --connect-timeout 1 --max-time 2 %s%s)" = "[]"`, base, path))
		} else {
			parts = append(parts, fmt.Sprintf(`curl -sf --connect-timeout 1 --max-time 2 %s%s >/dev/null`, base, path))
		}
	}
	for _, post := range spec.POSTProbes {
		body := strings.ReplaceAll(post.Body, `'`, `'\''`)
		parts = append(parts, fmt.Sprintf(`curl -sf --connect-timeout 1 --max-time 2 -X POST -H 'Content-Type: application/json' -d '%s' %s%s >/dev/null`, body, base, post.Path))
	}
	parts = append(parts, `(_gtsrv=$(cat .gt-smoke.pid 2>/dev/null); kill ${_gtsrv} 2>/dev/null || true; rm -f .gt-smoke.pid)`)
	return SmokeShellStrictPrefix + strings.Join(parts, " && ")
}

// IsProfileDerivedSmokeCommand reports whether cmd is the canonical probe built from rig docs.
func IsProfileDerivedSmokeCommand(cmd string) bool {
	lower := strings.ToLower(cmd)
	return strings.Contains(lower, ".gt-smoke.pid") &&
		strings.Contains(lower, "go run") &&
		strings.Contains(lower, "cmd/server") &&
		strings.Contains(lower, "--connect-timeout")
}
