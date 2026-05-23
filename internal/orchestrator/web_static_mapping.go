package orchestrator

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// WebStaticMapping describes how root-relative static URLs map to files under web/.
type WebStaticMapping struct {
	// StaticURLPrefix is e.g. "/static" when GET /static/{file} serves web/{file}.
	StaticURLPrefix string
	// RootServeStatic allows /file.js → web/file.js when architecture serves web at URL root.
	RootServeStatic bool
}

var (
	staticRouteGETRE   = regexp.MustCompile(`(?i)\bGET\s+/static/\{[^}]+\}`)
	staticRouteTableRE = regexp.MustCompile(`(?im)\|\s*GET\s*\|\s*/static/\{[^}]+\}\s*\|`)
	staticMapsWebRE    = regexp.MustCompile(`(?i)/static/\{[^}]+\}[^\n|]*\bweb/\{`)
	rootStaticGETRE    = regexp.MustCompile(`(?i)\bGET\s+/\{[^}]+\}[^\n|]*\bweb/`)
)

// ParseWebStaticMapping derives static URL rules from architecture/SPEC text.
func ParseWebStaticMapping(archText string) WebStaticMapping {
	m := WebStaticMapping{}
	lower := strings.ToLower(archText)
	hasStatic := staticRouteGETRE.MatchString(archText) ||
		staticRouteTableRE.MatchString(archText) ||
		staticMapsWebRE.MatchString(archText) ||
		(strings.Contains(lower, "/static/{file}") && strings.Contains(lower, "web/{file}"))
	if hasStatic {
		m.StaticURLPrefix = "/static"
	}
	if rootStaticGETRE.MatchString(archText) ||
		(strings.Contains(lower, "web/") && strings.Contains(lower, "get /") && !hasStatic &&
			(strings.Contains(lower, "/app.js") || strings.Contains(lower, "at url root") || strings.Contains(lower, "serves `web/`"))) {
		m.RootServeStatic = true
	}
	if m.StaticURLPrefix == "" {
		m.RootServeStatic = true
	}
	return m
}

// LoadWebStaticMappingFromRig reads rig docs the same way smoke spec loading does.
func LoadWebStaticMappingFromRig(townRoot, rig string, _ WorkflowValidation) WebStaticMapping {
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	var merged strings.Builder
	for _, name := range []string{"architecture.md", "SPEC.md", "plan.md"} {
		data, err := os.ReadFile(filepath.Join(rigDir, name))
		if err != nil {
			continue
		}
		merged.Write(data)
		merged.WriteByte('\n')
	}
	return ParseWebStaticMapping(merged.String())
}

// WebDiskPathForURLRef maps a root-relative src/href to a path under webRoot.
// Relative refs resolve from the HTML file directory under webRoot.
func (m WebStaticMapping) WebDiskPathForURLRef(webRoot, htmlRel, ref string) string {
	ref = normalizeWebURLRef(ref)
	if ref == "" || strings.Contains(ref, "..") {
		return ""
	}
	if strings.HasPrefix(ref, "/") {
		if m.StaticURLPrefix != "" && strings.HasPrefix(ref, m.StaticURLPrefix+"/") {
			rest := strings.TrimPrefix(ref, m.StaticURLPrefix)
			rest = strings.TrimPrefix(rest, "/")
			if rest == "" {
				return ""
			}
			return filepath.Join(webRoot, filepath.FromSlash(rest))
		}
		if m.StaticURLPrefix != "" && !m.RootServeStatic {
			return ""
		}
		return filepath.Join(webRoot, filepath.FromSlash(strings.TrimPrefix(ref, "/")))
	}
	htmlDir := filepath.Dir(filepath.Join(webRoot, filepath.Base(filepath.ToSlash(htmlRel))))
	return filepath.Join(htmlDir, filepath.FromSlash(ref))
}

func normalizeWebURLRef(ref string) string {
	ref = strings.TrimSpace(strings.Split(ref, "?")[0])
	return strings.Split(ref, "#")[0]
}

// StaticRefMismatchHint returns a validation hint when ref disagrees with architecture mapping.
func (m WebStaticMapping) StaticRefMismatchHint(ref string) string {
	ref = normalizeWebURLRef(ref)
	if ref == "" || !strings.HasPrefix(ref, "/") {
		return ""
	}
	if m.StaticURLPrefix == "" {
		return ""
	}
	if strings.HasPrefix(ref, m.StaticURLPrefix+"/") {
		return ""
	}
	lower := strings.ToLower(ref)
	if !strings.HasSuffix(lower, ".js") && !strings.HasSuffix(lower, ".css") {
		return ""
	}
	suggested := m.StaticURLPrefix + ref
	return "architecture defines static assets under " + m.StaticURLPrefix + "/ (use " + suggested + ", not " + ref + ")"
}
