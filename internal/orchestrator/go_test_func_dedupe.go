package orchestrator

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

// DuplicateGoTestFuncNames returns Test* function names declared more than once in src.
func DuplicateGoTestFuncNames(src []byte) []string {
	names := goTestFuncNamesInSource(src)
	if len(names) == 0 {
		return nil
	}
	counts := map[string]int{}
	for _, n := range names {
		counts[n]++
	}
	var dupes []string
	for name, n := range counts {
		if n > 1 {
			dupes = append(dupes, name)
		}
	}
	sort.Strings(dupes)
	return dupes
}

func goTestFuncNamesInSource(src []byte) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "file.go", src, parser.ParseComments)
	if err != nil {
		return nil
	}
	var names []string
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		if strings.HasPrefix(fn.Name.Name, "Test") {
			names = append(names, fn.Name.Name)
		}
	}
	return names
}

type goTestFuncSpan struct {
	name  string
	start int
	end   int
}

// DedupeGoTestFuncs removes later duplicate Test* function declarations (keeps the first).
func DedupeGoTestFuncs(src []byte) (out []byte, removed []string, err error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "file.go", src, parser.ParseComments)
	if err != nil {
		return src, nil, err
	}
	seen := map[string]bool{}
	var drop []goTestFuncSpan
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		name := fn.Name.Name
		if !strings.HasPrefix(name, "Test") {
			continue
		}
		start := fset.Position(fn.Pos()).Offset
		end := fset.Position(fn.End()).Offset
		if start < 0 || end < 0 || end > len(src) {
			continue
		}
		if seen[name] {
			drop = append(drop, goTestFuncSpan{name: name, start: start, end: end})
			removed = append(removed, name)
		} else {
			seen[name] = true
		}
	}
	if len(drop) == 0 {
		return src, nil, nil
	}
	sort.Slice(drop, func(i, j int) bool { return drop[i].start > drop[j].start })
	out = append([]byte(nil), src...)
	for _, sp := range drop {
		out = append(out[:sp.start], out[sp.end:]...)
	}
	return out, removed, nil
}

// NormalizeGoTestFileContent dedupes duplicate Test* funcs in *_test.go before syntax check.
func NormalizeGoTestFileContent(relPath string, content []byte) (normalized []byte, removed []string, err error) {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	if !strings.HasSuffix(relPath, "_test.go") {
		return content, nil, nil
	}
	dupes := DuplicateGoTestFuncNames(content)
	if len(dupes) == 0 {
		return content, nil, nil
	}
	out, removed, err := DedupeGoTestFuncs(content)
	if err != nil {
		return content, nil, fmt.Errorf("duplicate test functions %v: %w", dupes, err)
	}
	if len(DuplicateGoTestFuncNames(out)) > 0 {
		return content, nil, fmt.Errorf("duplicate test functions remain after dedupe: %v", DuplicateGoTestFuncNames(out))
	}
	return out, removed, nil
}
