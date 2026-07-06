package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	sqlOpenRE      = regexp.MustCompile(`(\w+)\s*,\s*err\s*:=\s*sql\.Open\(`)
	pkgInitSchemaRE = regexp.MustCompile(`(\w+)\.(\w+)\((\w+)`)
	pkgDBAssignRE   = regexp.MustCompile(`(\w+)\.DB\s*=\s*(\w+)`)
)

// TryAutoFixMainGoStoreDB patches cmd/server/main.go when it opens a DB via sql.Open
// but never assigns a package-level *sql.DB variable (e.g. store.DB = db).
// Uses a generic pattern: finds the sql.Open local variable, finds where it's
// passed to a package function (e.g. store.InitSchema(db)), and adds the missing
// package.DB = localVar assignment. Works with any package name.
func TryAutoFixMainGoStoreDB(rigDir string, v WorkflowValidation) (string, error) {
	mainRel := mainGoRelPath(v)
	if mainRel == "" {
		return "", nil
	}
	abs := filepath.Join(rigDir, filepath.FromSlash(mainRel))
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	src := string(data)

	dbVar := extractSQLiteOpenVar(src)
	if dbVar == "" {
		return "", nil
	}

	for _, m := range pkgInitSchemaRE.FindAllStringSubmatch(src, -1) {
		if len(m) < 4 {
			continue
		}
		pkg := m[1]
		_ = m[2] // func name (e.g. InitSchema)
		arg := m[3]
		if arg != dbVar {
			continue
		}
		// Check if assignment already exists
		alreadyAssigned := false
		for _, am := range pkgDBAssignRE.FindAllStringSubmatch(src, -1) {
			if len(am) >= 3 && am[1] == pkg && am[2] == dbVar {
				alreadyAssigned = true
				break
			}
		}
		if alreadyAssigned {
			continue
		}
		// Find insertion point after the pkg.FuncName(db) block
		needle := pkg + "." + m[2] + "(" + dbVar + ")"
		idx := strings.Index(src, needle)
		if idx < 0 {
			continue
		}
		after := src[idx+len(needle):]
		closeIdx := strings.Index(after, "\n\t}")
		if closeIdx < 0 {
			continue
		}
		insertAt := idx + len(needle) + closeIdx + len("\n\t}")
		out := src[:insertAt] + "\n\t" + pkg + ".DB = " + dbVar + "\n" + src[insertAt:]
		if string(data) == out {
			continue
		}
		if err := os.WriteFile(abs, []byte(out), 0o644); err != nil {
			return mainRel, err
		}
		return mainRel, nil
	}
	return "", nil
}

func extractSQLiteOpenVar(src string) string {
	// sql.Open can be assigned to a single var or a tuple.
	// Match: db, err := sql.Open(...)  OR  var db, err = sql.Open(...)
	matches := sqlOpenRE.FindStringSubmatch(src)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	// Also check: db = sql.Open(...)  or  db, _ = sql.Open(...)
	altRE := regexp.MustCompile(`(\w+)\s*[=:]=?\s*sql\.Open\(`)
	altMatches := altRE.FindStringSubmatch(src)
	if len(altMatches) >= 2 {
		return strings.TrimSpace(altMatches[1])
	}
	return ""
}

// TryAutoFixMainGoStoreDBFromOutput patches main.go when smoke output contains a nil-DB panic.
func TryAutoFixMainGoStoreDBFromOutput(rigDir string, v WorkflowValidation, smokeOutput string) (string, error) {
	if !strings.Contains(smokeOutput, "invalid memory address") && !strings.Contains(smokeOutput, "nil pointer") {
		return "", nil
	}
	if !strings.Contains(smokeOutput, "*sql.DB") && !strings.Contains(smokeOutput, "sql.DB") && !strings.Contains(smokeOutput, ".DB.") {
		return "", nil
	}
	return TryAutoFixMainGoStoreDB(rigDir, v)
}

func mainGoRelPath(v WorkflowValidation) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		return ""
	}
	check := func(f string) bool {
		f = filepath.ToSlash(strings.TrimSpace(f))
		return f == layout+"/cmd/server/main.go" || strings.HasSuffix(f, "/cmd/server/main.go")
	}
	for _, f := range v.UnionRequiredFiles() {
		if check(f) {
			return f
		}
	}
	for _, phase := range v.DeliveryPhases {
		for _, f := range phase.RequiredFiles {
			if check(f) {
				return f
			}
		}
	}
	if layout != "" {
		return layout + "/cmd/server/main.go"
	}
	return ""
}
