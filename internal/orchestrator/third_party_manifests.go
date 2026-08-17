package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// thirdPartyDeclared holds the union of third-party dependencies declared in a
// rig's manifests. Missing-import detection consults these sets before falling
// back to environment probes, so a package declared in package.json / go.mod /
// pyproject.toml is never reported as a missing implementation module even when
// it has not been installed yet (node_modules absent, venv not built, etc.).
type thirdPartyDeclared struct {
	goModules   map[string]bool
	nodePackage map[string]bool
	pythonRoot  map[string]bool
}

// loadDeclaredThirdPartyModules scans rigDir (skipping dependency/vendor dirs)
// for package.json, go.mod, pyproject.toml and requirements.txt and collects the
// declared dependency names.
func loadDeclaredThirdPartyModules(rigDir string) *thirdPartyDeclared {
	d := &thirdPartyDeclared{
		goModules:   make(map[string]bool),
		nodePackage: make(map[string]bool),
		pythonRoot:  make(map[string]bool),
	}
	_ = filepath.WalkDir(rigDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "node_modules", ".git", ".venv", "venv", "__pycache__", "dist", "build", ".next", ".cache":
				return filepath.SkipDir
			}
			if strings.HasPrefix(entry.Name(), ".venv") || strings.HasPrefix(entry.Name(), "venv") {
				return filepath.SkipDir
			}
			return nil
		}
		switch entry.Name() {
		case "package.json":
			collectNodePackage(d, path)
		case "go.mod":
			collectGoMod(d, path)
		case "pyproject.toml":
			collectPyProject(d, path)
		case "requirements.txt", "requirements-dev.txt", "dev-requirements.txt":
			collectRequirements(d, path)
		}
		return nil
	})
	return d
}

// matches reports whether mod (an import path / module name) is a declared
// third-party dependency for the given source extension.
func (d *thirdPartyDeclared) matches(mod, ext string) bool {
	switch ext {
	case ".go":
		return d.goModuleMatch(mod)
	case ".ts", ".tsx":
		return d.nodeMatch(mod)
	case ".py":
		return d.pythonMatch(mod)
	}
	return false
}

// goModuleMatch matches import path prefixes against declared go.mod modules,
// e.g. module github.com/foo/bar covers github.com/foo/bar/subpkg.
func (d *thirdPartyDeclared) goModuleMatch(mod string) bool {
	for m := range d.goModules {
		if mod == m || strings.HasPrefix(mod, m+"/") {
			return true
		}
	}
	return false
}

// nodeMatch matches a bare import specifier against declared package.json
// dependencies. Handles subpaths (react-dom/client -> react-dom) and scoped
// packages (@scope/pkg/sub -> @scope/pkg).
func (d *thirdPartyDeclared) nodeMatch(mod string) bool {
	if d.nodePackage[mod] {
		return true
	}
	if strings.HasPrefix(mod, "@") {
		parts := strings.SplitN(mod, "/", 3)
		if len(parts) >= 2 && d.nodePackage[parts[0]+"/"+parts[1]] {
			return true
		}
		return false
	}
	if i := strings.IndexByte(mod, '/'); i > 0 {
		return d.nodePackage[mod[:i]]
	}
	return false
}

// pythonMatch matches the top-level module root (first dot segment), e.g.
// import fastapi.routing is satisfied by a declared dependency "fastapi".
func (d *thirdPartyDeclared) pythonMatch(mod string) bool {
	root := strings.SplitN(mod, ".", 2)[0]
	return d.pythonRoot[root]
}

func collectNodePackage(d *thirdPartyDeclared, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var pkg struct {
		Dependencies         map[string]string `json:"dependencies"`
		DevDependencies      map[string]string `json:"devDependencies"`
		PeerDependencies     map[string]string `json:"peerDependencies"`
		OptionalDependencies map[string]string `json:"optionalDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return
	}
	for _, m := range []map[string]string{
		pkg.Dependencies, pkg.DevDependencies,
		pkg.PeerDependencies, pkg.OptionalDependencies,
	} {
		for name := range m {
			if name = strings.TrimSpace(name); name != "" {
				d.nodePackage[name] = true
			}
		}
	}
}

var goModRequireLine = regexp.MustCompile(`^\s*([A-Za-z0-9._~/-]+)\s+v[0-9]`)

func collectGoMod(d *thirdPartyDeclared, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		// Single-line "require mod v1.2.3" puts the module after the keyword; the
		// block form lists bare "mod v1.2.3" lines. Strip a leading "require".
		if strings.HasPrefix(trimmed, "require") && !strings.HasPrefix(trimmed, "require (") {
			trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "require"))
		}
		if m := goModRequireLine.FindStringSubmatch("  " + trimmed); m != nil {
			d.goModules[m[1]] = true
		}
	}
}

func collectPyProject(d *thirdPartyDeclared, path string) {
	var doc struct {
		Project struct {
			Dependencies         []string            `toml:"dependencies"`
			OptionalDependencies map[string][]string `toml:"optional-dependencies"`
		} `toml:"project"`
	}
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		return
	}
	for _, spec := range doc.Project.Dependencies {
		addPythonDependency(d, spec)
	}
	for _, specs := range doc.Project.OptionalDependencies {
		for _, spec := range specs {
			addPythonDependency(d, spec)
		}
	}
}

func collectRequirements(d *thirdPartyDeclared, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		addPythonDependency(d, line)
	}
}

var pyDepRoot = regexp.MustCompile(`^([A-Za-z0-9_.-]+)`)

// addPythonDependency extracts the bare distribution name from a dependency
// spec like "fastapi>=0.100", "pydantic[email]==2.0" or "pkg @ url".
func addPythonDependency(d *thirdPartyDeclared, spec string) {
	line := strings.TrimSpace(spec)
	if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
		return
	}
	if strings.HasPrefix(line, "-r") || strings.HasPrefix(line, "--") {
		return
	}
	if strings.HasPrefix(line, "git+") || strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
		return
	}
	if i := strings.IndexAny(line, " [=<>!~;"); i >= 0 {
		line = line[:i]
	}
	if m := pyDepRoot.FindStringSubmatch(line); m != nil {
		name := strings.TrimSpace(m[1])
		// Normalize name: canonical Python distributions use '-' but imports use '_'.
		name = strings.ReplaceAll(name, "-", "_")
		if name != "" && name != "e" && name != "egg" {
			d.pythonRoot[name] = true
		}
	}
}

// declaredThirdPartyProbe wraps the declared-set check plus environment probe
// fallback so call sites get one consistent "is this importable third-party?"
func declaredThirdPartyProbe(d *thirdPartyDeclared, mod, rigDir, ext string) bool {
	if d != nil && d.matches(mod, ext) {
		return true
	}
	return isImportableThirdParty(mod, rigDir, ext)
}
