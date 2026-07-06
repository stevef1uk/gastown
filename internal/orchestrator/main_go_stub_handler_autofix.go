package orchestrator

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	inlineHandlerRE = regexp.MustCompile(`(?s)HandleFunc\(` + "`" + `([^` + "`" + `]+)` + "`" + `\s*,\s*func\s*\([^)]*\)\s*\{`)
	inlineHandlerSwitchRE = regexp.MustCompile(`(?m)\s*case\s+(http\.Method\w+|\"\w+\")\s*:\s*\n`)
)

// HandleInlineHandlerRefactoring detects inline stub handlers in cmd/server/main.go
// that return hardcoded JSON instead of delegating to the api handler package, and
// rewrites them to use proper delegation.
//
// Dectects HandleFunc calls where the handler body contains w.Write with hardcoded
// data and replaces each case body with a call to the matching handler method.
func HandleInlineHandlerRefactoring(rigDir string, v WorkflowValidation) (string, error) {
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

	if !hasStubInlineHandler(src) {
		return "", nil
	}

	handlerVar, pkgPath, err := findHandlerVariable(src)
	if err != nil || handlerVar == "" {
		return "", nil
	}

	// Try to read the handler package to find exported handler function names.
	handlerFuncs := findHandlerFuncs(rigDir, v, pkgPath)

	if len(handlerFuncs) == 0 {
		return "", nil
	}

	result := replaceInlineHandlers(src, handlerVar, handlerFuncs)
	if result == src {
		return "", nil
	}

	if err := os.WriteFile(abs, []byte(result), 0644); err != nil {
		return mainRel, err
	}
	return mainRel, nil
}

// hasStubInlineHandler checks if main.go has a HandleFunc with inline func that
// does w.Write with hardcoded data (a stub pattern).
func hasStubInlineHandler(src string) bool {
	if !strings.Contains(src, "HandleFunc") {
		return false
	}
	if !strings.Contains(src, "w.Write") {
		return false
	}
	if !strings.Contains(src, `[]byte("`) && !strings.Contains(src, "[]byte(`") {
		return false
	}
	if strings.Contains(src, ".ListLinks") || strings.Contains(src, ".CreateLink") ||
		strings.Contains(src, ".GetLink") || strings.Contains(src, ".UpdateLink") ||
		strings.Contains(src, ".DeleteLink") {
		// Already delegating — not a stub
		return false
	}
	return true
}

// findHandlerVariable looks for the handler struct variable in main.go, e.g.:
//
//	h := &api.Handler{DB: db}
//	h := api.Handler{DB: db}
//	var h = api.Handler{DB: db}
func findHandlerVariable(src string) (varName, pkgPath string, err error) {
	re := regexp.MustCompile(`(\w+)\s*:?=\s*(?:&\s*)?(\w+)\.(\w+)\s*\{`)
	matches := re.FindStringSubmatch(src)
	if len(matches) >= 4 {
		return matches[1], matches[2], nil
	}
	return "", "", nil
}

// findHandlerFuncs reads the handler package source files and collects exported
// function names that match the common handler pattern (two params: http.ResponseWriter, *http.Request).
func findHandlerFuncs(rigDir string, v WorkflowValidation, pkgImport string) map[string]bool {
	handlerDir := filepath.Join(rigDir, filepath.FromSlash(v.LayoutRoot), "internal", pkgImport)
	if fi, err := os.Stat(handlerDir); err != nil || !fi.IsDir() {
		// Try alternate locations: internal/api, internal/handler, etc.
		for _, alt := range []string{"internal/api", "internal/handler", "pkg/api", "pkg/handler"} {
			altDir := filepath.Join(rigDir, filepath.FromSlash(v.LayoutRoot), alt)
			if fi, err := os.Stat(altDir); err == nil && fi.IsDir() {
				handlerDir = altDir
				break
			}
		}
	}

	funcs := map[string]bool{}
	entries, err := os.ReadDir(handlerDir)
	if err != nil {
		return funcs
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(handlerDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, data, 0)
		if err != nil {
			continue
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !fn.Name.IsExported() {
				continue
			}
			// Handler functions take (http.ResponseWriter, *http.Request)
			if fn.Type.Params != nil && len(fn.Type.Params.List) == 2 {
				funcs[fn.Name.Name] = true
			}
		}
	}
	return funcs
}

// methodToHandlerPrefix guesses the handler prefix from HTTP method.
func methodToHandlerPrefix(method string) string {
	switch method {
	case "GET", `"GET"`, "http.MethodGet":
		return "List"
	case "POST", `"POST"`, "http.MethodPost":
		return "Create"
	case "DELETE", `"DELETE"`, "http.MethodDelete":
		return "Delete"
	case "PUT", `"PUT"`, "http.MethodPut":
		return "Update"
	case "PATCH", `"PATCH"`, "http.MethodPatch":
		return "Patch"
	default:
		return ""
	}
}

// resourceFromPath extracts the resource name from the URL path.
// e.g. /api/links → Links, /api/users → Users
func resourceFromPath(path string) string {
	path = strings.TrimRight(path, "/")
	segments := strings.Split(path, "/")
	if len(segments) == 0 {
		return ""
	}
	last := segments[len(segments)-1]
	// Singularise naively (remove trailing 's')
	singular := last
	if strings.HasSuffix(last, "ies") {
		singular = strings.TrimSuffix(last, "ies") + "y"
	} else if strings.HasSuffix(last, "es") {
		singular = strings.TrimSuffix(last, "es")
	} else if strings.HasSuffix(last, "s") {
		singular = strings.TrimSuffix(last, "s")
	}
	if singular == "" {
		return strings.ToUpper(last[:1]) + last[1:]
	}
	return strings.ToUpper(singular[:1]) + singular[1:]
}

// resourceFromHandlerFunc guesses the resource name from a handler function name.
// e.g. ListLinks → Links, CreateUser → User
func resourceFromHandlerFunc(name string) string {
	// Strip common prefixes
	for _, prefix := range []string{"List", "Create", "Delete", "Update", "Get", "Patch", "Handle"} {
		if strings.HasPrefix(name, prefix) {
			return name[len(prefix):]
		}
	}
	return ""
}

// replaceInlineHandlers scans src for HandleFunc inline handlers and replaces stub bodies.
func replaceInlineHandlers(src, handlerVar string, handlerFuncs map[string]bool) string {
	prefixFuncs := map[string][]string{}
	for fn := range handlerFuncs {
		for _, prefix := range []string{"List", "Create", "Delete", "Update", "Get", "Patch"} {
			if strings.HasPrefix(fn, prefix) {
				prefixFuncs[prefix] = append(prefixFuncs[prefix], fn)
				break
			}
		}
	}
	if len(prefixFuncs) == 0 {
		return src
	}

	result := src
	// Find each HandleFunc(ROUTE, func(...) { ... }) block
	start := 0
	for {
		idx := strings.Index(result[start:], "HandleFunc(")
		if idx < 0 {
			break
		}
		idx += start
		// Move past "HandleFunc("
		after := idx + len("HandleFunc(")
		// Read the route argument (backtick or double-quoted string)
		if after >= len(result) {
			break
		}
		var route string
		if result[after] == '`' {
			end := strings.Index(result[after+1:], "`")
			if end < 0 {
				break
			}
			route = result[after+1 : after+1+end]
			after = after + 1 + end + 1
		} else if result[after] == '"' {
			end := strings.Index(result[after+1:], `"`)
			if end < 0 {
				break
			}
			route = result[after+1 : after+1+end]
			after = after + 1 + end + 1
		} else {
			break
		}
		// Expect comma
		if after >= len(result) || result[after] != ',' {
			break
		}
		after++ // skip comma

		// Expect func keyword
		funcIdx := strings.Index(result[after:], "func(")
		if funcIdx < 0 {
			break
		}
		after += funcIdx
		// Find the opening brace of the func body (after the func signature)
		braceIdx := strings.Index(result[after:], "{")
		if braceIdx < 0 {
			break
		}
		bodyStart := after + braceIdx

		// Brace-count to find the matching close brace
		depth := 0
		bodyEnd := -1
		for i := bodyStart; i < len(result); i++ {
			if result[i] == '{' {
				depth++
			} else if result[i] == '}' {
				depth--
				if depth == 0 {
					bodyEnd = i
					break
				}
			}
		}
		if bodyEnd < 0 {
			break
		}

		handlerBody := result[bodyStart : bodyEnd+1]

		newBody := replaceCases(handlerBody, handlerVar, prefixFuncs)
		if newBody == handlerBody {
			start = bodyEnd + 1
			continue
		}

		result = result[:idx] + "HandleFunc(" + route + ", " + newBody + ")" + result[bodyEnd+1:]
		start = idx + len("HandleFunc(") + len(route) + 2 + len(newBody) + 1
	}
	return result
}

// replaceCases rewrites a switch-on-method handler body, replacing stub cases with
// delegation calls.
func replaceCases(handlerBody, handlerVar string, prefixFuncs map[string][]string) string {
	lines := strings.Split(handlerBody, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "case ") && strings.HasSuffix(trimmed, ":") {
			methodPart := strings.TrimPrefix(trimmed, "case ")
			methodPart = strings.TrimSuffix(methodPart, ":")
			methodPart = strings.TrimSpace(methodPart)

			prefix := methodToHandlerPrefix(methodPart)
			if prefix == "" {
				out = append(out, line)
				i++
				continue
			}

			funcs := prefixFuncs[prefix]
			if len(funcs) == 0 {
				out = append(out, line)
				i++
				continue
			}
			funcName := funcs[0]

			indent := ""
			for _, r := range line {
				if r == '\t' || r == ' ' {
					indent += string(r)
				} else {
					break
				}
			}

			out = append(out, line)
			i++
			for i < len(lines) {
				nextTrim := strings.TrimSpace(lines[i])
				if strings.HasPrefix(nextTrim, "case ") || nextTrim == "}" || nextTrim == "default:" {
					out = append(out, indent+"\t"+handlerVar+"."+funcName+"(w, r)")
					break
				}
				i++
			}
		} else {
			out = append(out, line)
			i++
		}
	}
	return strings.Join(out, "\n")
}
