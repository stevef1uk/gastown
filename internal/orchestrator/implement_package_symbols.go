package orchestrator

import (
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

type goExportedSymbols struct {
	Types []string
	Funcs []string
}

func sameGoImplementPackage(a, b, layoutRoot string) bool {
	pa := GoBuildRelPackage(layoutRoot, a)
	pb := GoBuildRelPackage(layoutRoot, b)
	return pa != "" && pa == pb
}

func earlierSamePackageFiles(relPath string, v WorkflowValidation) []string {
	var out []string
	for _, p := range EarlierRequiredFilesForBead(relPath, v.RequiredFiles) {
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		if sameGoImplementPackage(p, relPath, v.LayoutRoot) {
			out = append(out, p)
		}
	}
	return out
}

func laterSamePackageProductionFiles(relPath string, v WorkflowValidation) []string {
	relPath = filepath.ToSlash(strings.TrimSpace(relPath))
	activeScore := implementationPathScore(relPath)
	var out []string
	for _, p := range OrderRequiredFilesForImplementation(v.RequiredFiles) {
		p = filepath.ToSlash(strings.TrimSpace(p))
		if p == "" || p == relPath || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			continue
		}
		if implementationPathScore(p) <= activeScore {
			continue
		}
		if sameGoImplementPackage(p, relPath, v.LayoutRoot) {
			out = append(out, p)
		}
	}
	return out
}

func readExportedGoSymbolsFromRig(rigDir, relPath string) goExportedSymbols {
	path := filepath.Join(rigDir, filepath.FromSlash(relPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return goExportedSymbols{}
	}
	return splitExportedGoSymbols(string(data))
}

func splitExportedGoSymbols(src string) goExportedSymbols {
	seenT := map[string]bool{}
	seenF := map[string]bool{}
	var types, funcs []string
	for _, m := range goExportedTypeRE.FindAllStringSubmatch(src, -1) {
		if len(m) >= 2 && !seenT[m[1]] {
			seenT[m[1]] = true
			types = append(types, m[1])
		}
	}
	for _, m := range goExportedFuncRE.FindAllStringSubmatch(src, -1) {
		if len(m) >= 2 && !seenF[m[1]] {
			seenF[m[1]] = true
			funcs = append(funcs, m[1])
		}
	}
	return goExportedSymbols{Types: types, Funcs: funcs}
}

func exportedSymbolsInContent(src string) goExportedSymbols {
	return splitExportedGoSymbols(src)
}

func symbolsDefinedOnEarlierSiblings(rigDir, relPath string, v WorkflowValidation) goExportedSymbols {
	seenT := map[string]bool{}
	seenF := map[string]bool{}
	var types, funcs []string
	for _, sib := range earlierSamePackageFiles(relPath, v) {
		sym := readExportedGoSymbolsFromRig(rigDir, sib)
		for _, n := range sym.Types {
			if !seenT[n] {
				seenT[n] = true
				types = append(types, n)
			}
		}
		for _, n := range sym.Funcs {
			if !seenF[n] {
				seenF[n] = true
				funcs = append(funcs, n)
			}
		}
	}
	return goExportedSymbols{Types: types, Funcs: funcs}
}

func symbolsOwnedByLaterSiblings(rigDir, relPath string, v WorkflowValidation) goExportedSymbols {
	seenT := map[string]bool{}
	seenF := map[string]bool{}
	var types, funcs []string
	for _, sib := range laterSamePackageProductionFiles(relPath, v) {
		sym := readExportedGoSymbolsFromRig(rigDir, sib)
		if len(sym.Types) == 0 && len(sym.Funcs) == 0 {
			// Use ownership-table "Owns" column only — dependency columns list symbols this
			// bead must call (e.g. InitSchema on store.go) and must not block schema beads.
			owned := architectureOwnedSymbolsForBead(rigDir, sib, v)
			for _, n := range owned.Types {
				if !seenT[n] {
					seenT[n] = true
					types = append(types, n)
				}
			}
			for _, n := range owned.Funcs {
				if !seenF[n] {
					seenF[n] = true
					funcs = append(funcs, n)
				}
			}
		}
		for _, n := range sym.Types {
			if !seenT[n] {
				seenT[n] = true
				types = append(types, n)
			}
		}
		for _, n := range sym.Funcs {
			if !seenF[n] {
				seenF[n] = true
				funcs = append(funcs, n)
			}
		}
	}
	return goExportedSymbols{Types: types, Funcs: funcs}
}

func astIsExportedName(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// PrepareImplementPackageWrite strips exported type redeclarations already present on earlier same-package implement files.
func PrepareImplementPackageWrite(mayorRigDir, relPath, content string, v WorkflowValidation) (string, bool) {
	if !WorkflowUsesGo(v) || !strings.HasSuffix(relPath, ".go") {
		return content, false
	}
	earlier := symbolsDefinedOnEarlierSiblings(mayorRigDir, relPath, v)
	if len(earlier.Types) == 0 {
		return content, false
	}
	want := map[string]bool{}
	for _, n := range earlier.Types {
		want[n] = true
	}
	out, ok := stripExportedTypeDecls(content, want)
	return out, ok
}

func stripExportedTypeDecls(src string, typeNames map[string]bool) (string, bool) {
	if len(typeNames) == 0 {
		return src, false
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "write.go", src, parser.ParseComments)
	if err != nil {
		return src, false
	}
	stripped := false
	var decls []ast.Decl
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			decls = append(decls, d)
			continue
		}
		var specs []ast.Spec
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || !typeNames[ts.Name.Name] || !ast.IsExported(ts.Name.Name) {
				specs = append(specs, spec)
				continue
			}
			stripped = true
		}
		if len(specs) == 0 {
			continue
		}
		gd.Specs = specs
		decls = append(decls, gd)
	}
	if !stripped {
		return src, false
	}
	f.Decls = decls
	var b strings.Builder
	if err := format.Node(&b, fset, f); err != nil {
		return src, false
	}
	return b.String(), true
}

func packageHasSchemaOwner(relPath string, v WorkflowValidation) bool {
	for _, p := range v.RequiredFiles {
		if sameGoImplementPackage(p, relPath, v.LayoutRoot) && IsSQLiteSchemaBeadPath(p) {
			return true
		}
	}
	return false
}
