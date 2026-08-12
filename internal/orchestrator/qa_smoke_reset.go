package orchestrator

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Smoke reset paths are relative to the runtime smoke workDir (usually layout_root).
// They come from rig docs, not hardcoded per rig.

var (
	smokeResetHeadingRE = regexp.MustCompile(`(?im)^##\s+.*\bsmoke\s+reset\b`)
	smokeResetBulletRE  = regexp.MustCompile(`^\s*[-*]\s+(.+?)\s*$`)
	smokeResetKVRE      = regexp.MustCompile(`(?im)^\s*smoke_reset:\s*(\S+)\s*$`)
	docBacktickDBRE     = regexp.MustCompile("`(\\.?/?[a-zA-Z0-9][a-zA-Z0-9_.-]*\\.(?:db|sqlite3?|wal))`")
	sqlOpenPathRE       = regexp.MustCompile(`(?i)sql\.Open\([^,]+,\s*["']([^"']+)["']`)
	relativePersistRE   = regexp.MustCompile(`(?:^|[\s"'(])(\.?/?[a-zA-Z0-9][a-zA-Z0-9_.-]*\.(?:db|sqlite3?|wal))\b`)
	goRunServerRE       = regexp.MustCompile(`(?i)\bgo\s+run\s+(?:\./)?cmd/server(?:/main\.go)?\b`)
	goRunMainRE         = regexp.MustCompile(`(?i)\bgo\s+run\s+(?:\./)?(?:main\.go|.)\b`)
)

func enrichAPISmokeSpec(spec *APISmokeSpec, mergedDocs string, v WorkflowValidation) {
	if spec == nil {
		return
	}
	spec.ResetPaths = collectSmokeResetPaths(mergedDocs, *spec)
	if spec.ServerStart == "" {
		spec.ServerStart = deriveRuntimeSmokeServerStart(v, mergedDocs)
	}
}

// collectSmokeResetPaths merges explicit doc sections with inferred persistence files
// when the API contract requires an empty JSON array on a fresh server.
func collectSmokeResetPaths(mergedDocs string, spec APISmokeSpec) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = normalizeSmokeResetPath(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range parseSmokeResetSection(mergedDocs) {
		add(p)
	}
	for _, m := range smokeResetKVRE.FindAllStringSubmatch(mergedDocs, -1) {
		if len(m) >= 2 {
			add(m[1])
		}
	}
	if len(spec.GETEmptyJSONArray) > 0 {
		for _, p := range inferSmokeResetPathsFromDocs(mergedDocs) {
			add(p)
		}
	}
	sort.Strings(out)
	return out
}

func parseSmokeResetSection(text string) []string {
	loc := smokeResetHeadingRE.FindStringIndex(text)
	if loc == nil {
		return nil
	}
	rest := text[loc[1]:]
	if i := strings.Index(rest, "\n## "); i >= 0 {
		rest = rest[:i]
	}
	var paths []string
	for _, line := range strings.Split(rest, "\n") {
		if m := smokeResetBulletRE.FindStringSubmatch(line); len(m) >= 2 {
			paths = append(paths, strings.TrimSpace(m[1]))
		}
	}
	return paths
}

func inferSmokeResetPathsFromDocs(text string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = normalizeSmokeResetPath(p)
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, m := range docBacktickDBRE.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			add(m[1])
		}
	}
	for _, m := range sqlOpenPathRE.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			add(m[1])
		}
	}
	for _, m := range relativePersistRE.FindAllStringSubmatch(text, -1) {
		if len(m) >= 2 {
			add(m[1])
		}
	}
	return out
}

func normalizeSmokeResetPath(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "`\"'")
	p = filepath.ToSlash(p)
	if p == "" || p == "." {
		return ""
	}
	lower := strings.ToLower(p)
	if strings.Contains(lower, ":memory:") || strings.Contains(p, "..") {
		return ""
	}
	if filepath.IsAbs(p) {
		return ""
	}
	p = strings.TrimPrefix(p, "./")
	return p
}

// deriveRuntimeSmokeServerStart returns the background server command for HTTP smoke.
// Order: ## Runtime smoke server in docs → profile QA command → Go default.
func deriveRuntimeSmokeServerStart(v WorkflowValidation, mergedDocs string) string {
	if cmd := parseSmokeServerSection(mergedDocs); cmd != "" {
		return cmd
	}
	if WorkflowUsesGo(v) {
		for _, src := range []string{v.QAVerifyCommand, v.ActivePhaseQAVerifyCommand()} {
			if m := goRunServerRE.FindString(src); m != "" {
				return strings.TrimSpace(m)
			}
			// Also check for go run main.go or go run .
			if m := goRunMainRE.FindString(src); m != "" {
				return strings.TrimSpace(m)
			}
		}
		// Try to find main.go in layout root: cmd/server/main.go, main.go, layout/main.go
		layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
		if layout != "" && layout != "." {
			// Use relative path from layout root (working dir will be layout root)
			return "go run ./cmd/server/main.go"
		}
		return "go run main.go"
	}
	if WorkflowUsesPython(v) {
		if cmd := extractPythonServerStartFromQA(v); cmd != "" {
			return cmd
		}
		if cmd := ExtractPythonServerStartFromText(mergedDocs); cmd != "" {
			return cmd
		}
		// Fallback: derive from layout root + main.py/app.py presence (mirrors Go fallback)
		if cmd := derivePythonServerStartFromLayout(v); cmd != "" {
			return cmd
		}
	}
	return ""
}

func smokeResetShellParts(paths []string) []string {
	var parts []string
	for _, p := range paths {
		p = normalizeSmokeResetPath(p)
		if p == "" {
			continue
		}
		if m := smokeStepMarker("reset:" + p); m != "" {
			parts = append(parts, m)
		}
		q := bashSingleQuote(p)
		if strings.HasSuffix(p, "/") {
			parts = append(parts, "rm -rf -- "+q)
		} else {
			parts = append(parts, "rm -f -- "+q)
		}
	}
	return parts
}

func bashSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// derivePythonServerStartFromLayout derives a uvicorn command from the layout root
// and the presence of main.py/app.py/server.py, mirroring the Go fallback logic.
func derivePythonServerStartFromLayout(v WorkflowValidation) string {
	if !WorkflowUsesPython(v) {
		return ""
	}
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" || layout == "." {
		return ""
	}
	// Look for common Python server entry patterns in required files
	for _, f := range v.RequiredFiles {
		f = filepath.ToSlash(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		// Remove layout root prefix
		rel := f
		if strings.HasPrefix(f, layout+"/") {
			rel = strings.TrimPrefix(f, layout+"/")
		}
		lower := strings.ToLower(rel)
		if strings.HasSuffix(lower, "/main.py") ||
			strings.HasSuffix(lower, "/app.py") ||
			strings.HasSuffix(lower, "/server.py") {
			// Derive module from path: e.g., backend/app/main.py → backend.app.main
			dir := filepath.Dir(rel)
			parts := strings.Split(dir, "/")
			var modParts []string
			for _, part := range parts {
				if part != "" {
					modParts = append(modParts, part)
				}
			}
			if len(modParts) >= 1 {
				fileName := strings.TrimSuffix(filepath.Base(f), ".py")
				// Module is the package path (dots) + filename
				modPath := strings.Join(modParts, ".")
				module := modPath + "." + fileName
				return fmt.Sprintf("uvicorn %s:app --host 0.0.0.0 --port 8000", module)
			}
		}
	}
	// Fallback for common layouts: layout/backend/app/main.py → backend.app.main
	commonPaths := []string{
		"backend/app/main.py",
		"app/main.py",
		"server/main.py",
		"src/app/main.py",
		"backend/main.py",
	}
	for _, cp := range commonPaths {
		for _, f := range v.RequiredFiles {
			f = filepath.ToSlash(strings.TrimSpace(f))
			if strings.HasSuffix(f, layout+"/"+cp) || strings.HasSuffix(f, cp) {
				dir := filepath.Dir(cp)
				parts := strings.Split(dir, "/")
				var modParts []string
				for _, part := range parts {
					if part != "" {
						modParts = append(modParts, part)
					}
				}
				fileName := strings.TrimSuffix(filepath.Base(cp), ".py")
				modPath := strings.Join(modParts, ".")
				module := modPath + "." + fileName
				return fmt.Sprintf("uvicorn %s:app --host 0.0.0.0 --port 8000", module)
			}
		}
	}
	return ""
}
