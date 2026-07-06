package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	pyModuleLevelNoneRE = regexp.MustCompile(`(?m)^(\w+)\s*=\s*None\s*$`)
	pySQLiteOpenRE      = regexp.MustCompile(`\bsqlite3\.connect\(`)
	pyCreateEngineRE    = regexp.MustCompile(`\bcreate_engine\(`)
	pyGlobalRE          = regexp.MustCompile(`(?m)^\s*global\s+(\w+(?:\s*,\s*\w+)*)\s*$`)
	pyFunctionDefRE     = regexp.MustCompile(`(?m)^(?:async\s+)?def\s+(\w+)\s*\([^)]*\)\s*:`)
)

func isPyDBInitCall(src string) bool {
	return pySQLiteOpenRE.MatchString(src) || pyCreateEngineRE.MatchString(src)
}

// collectPyModuleLevelNoneVar scans a Python source for module-level `name = None`.
// Only matches non-indented (column 0) assignments to avoid matching inside functions/classes.
func collectPyModuleLevelNoneVar(src string) []string {
	var vars []string
	seen := map[string]bool{}
	// pyModuleLevelNoneRE uses ^ at column 0 so indented lines won't match.
	for _, m := range pyModuleLevelNoneRE.FindAllStringSubmatch(src, -1) {
		name := strings.TrimSpace(m[1])
		if !seen[name] {
			seen[name] = true
			vars = append(vars, name)
		}
	}
	return vars
}

// TryAutoFixMainPyStoreDB patches a Python entrypoint file when it declares a module-level
// `db = None` (or similar) and has a function that initialises a DB connection locally without
// using `global` to assign to the module-level variable.
//
// Detected patterns:
//   - `db = None` at module level and a function body calls `sqlite3.connect(...)` or `create_engine(...)`
//     and assigns to a local variable with the same name but does not declare `global db`.
func TryAutoFixMainPyStoreDB(rigDir string, v WorkflowValidation) (string, error) {
	entry := pyEntrypointRelPath(v)
	if entry == "" {
		return "", nil
	}
	abs := filepath.Join(rigDir, filepath.FromSlash(entry))
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	src := string(data)

	noneVars := collectPyModuleLevelNoneVar(src)
	if len(noneVars) == 0 {
		return "", nil
	}

	funcStartRE := regexp.MustCompile(`(?m)^(async\s+)?def\s+\w+\s*\(`)
	var fixed bool
	for _, loc := range funcStartRE.FindAllStringIndex(src, -1) {
		fnStart := loc[0]
		// Find the body start (after the colon)
		bodyStart := strings.IndexByte(src[fnStart:], ':')
		if bodyStart < 0 {
			continue
		}
		bodyStart += fnStart + 1
		fnBody := src[bodyStart:]

		if !isPyDBInitCall(fnBody) {
			continue
		}

		// Collect existing global declarations in this function
		globalNames := map[string]bool{}
		for _, gm := range pyGlobalRE.FindAllStringSubmatch(fnBody, -1) {
			for _, part := range strings.Split(gm[1], ",") {
				globalNames[strings.TrimSpace(part)] = true
			}
		}

		// Check each function line for assignments to module-level None vars
		for _, nv := range noneVars {
			if globalNames[nv] {
				continue
			}
			// Check if the function assigns to this name: `name = ...` (indented)
			assignRE := regexp.MustCompile(`(?m)^[ \t]+` + regexp.QuoteMeta(nv) + `\s*=\s*`)
			if !assignRE.MatchString(fnBody) {
				continue
			}
			// Need to add `global nv`. Find first line of function body.
			firstBodyLine := strings.IndexFunc(fnBody, func(r rune) bool { return r != ' ' && r != '\t' && r != '\n' })
			if firstBodyLine < 0 {
				continue
			}
			insertAt := bodyStart + firstBodyLine
			indent := ""
			for _, r := range src[bodyStart:] {
				if r == ' ' || r == '\t' {
					indent += string(r)
				} else {
					break
				}
			}
			globalStmt := indent + "global " + nv + "\n"
			out := src[:insertAt] + globalStmt + src[insertAt:]
			if out == src {
				continue
			}
			if err := os.WriteFile(abs, []byte(out), 0o644); err != nil {
				return entry, err
			}
			src = out // re-read for subsequent fixes
			fixed = true
		}
	}
	if fixed {
		return entry, nil
	}
	return "", nil
}

// TryAutoFixMainPyStoreDBFromOutput patches Python entrypoint when smoke output contains
// a NoneType attribute error involving a DB operation.
func TryAutoFixMainPyStoreDBFromOutput(rigDir string, v WorkflowValidation, smokeOutput string) (string, error) {
	if !strings.Contains(smokeOutput, "NoneType") {
		return "", nil
	}
	if !strings.Contains(smokeOutput, "object has no attribute") {
		return "", nil
	}
	// Match DB-related NoneType errors
	if !strings.Contains(smokeOutput, "sqlite") &&
		!strings.Contains(smokeOutput, "execute") &&
		!strings.Contains(smokeOutput, "cursor") &&
		!strings.Contains(smokeOutput, "database") &&
		!strings.Contains(smokeOutput, "engine") &&
		!strings.Contains(smokeOutput, "rollback") &&
		!strings.Contains(smokeOutput, "commit") &&
		!strings.Contains(smokeOutput, "query") {
		return "", nil
	}
	return TryAutoFixMainPyStoreDB(rigDir, v)
}

// pyEntrypointRelPath returns the main Python entrypoint path relative to rig dir,
// based on `v.LayoutRoot` and the files listed in the specification.
func pyEntrypointRelPath(v WorkflowValidation) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		return ""
	}
	entryCandidates := []string{"main.py", "app.py", "server.py"}
	for _, ec := range entryCandidates {
		candidate := layout + "/" + ec
		for _, f := range v.RequiredFiles {
			if f == candidate || strings.HasSuffix(f, "/"+ec) {
				return f
			}
		}
		for _, phase := range v.DeliveryPhases {
			for _, f := range phase.RequiredFiles {
				if f == candidate || strings.HasSuffix(f, "/"+ec) {
					return f
				}
			}
		}
	}
	// Fallback to union
	for _, ec := range entryCandidates {
		for _, f := range v.UnionRequiredFiles() {
			if f == ec || strings.HasSuffix(f, "/"+ec) {
				return f
			}
		}
		// Full path with layout
		candidate := layout + "/" + ec
		for _, f := range v.UnionRequiredFiles() {
			if f == candidate {
				return f
			}
		}
	}
	if layout != "" {
		return layout + "/main.py"
	}
	return ""
}
