package orchestrator

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const smokeStepMarkerPrefix = "GT_SMOKE:"

var smokeStepMarkerRE = regexp.MustCompile(smokeStepMarkerPrefix + `([^\s]+)`)

// SmokeFailureDetail is parsed from labeled runtime smoke output (GT_SMOKE markers).
type SmokeFailureDetail struct {
	FailedStep string // e.g. GET:/, GET:/static/app.js, wait_root
	RawTail    string // last lines of smoke output
}

// smokeStepMarker emits a stderr label before the next probe (parsed on failure).
func smokeStepMarker(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	return fmt.Sprintf(`(echo %s%s >&2)`, smokeStepMarkerPrefix, label)
}

// ParseSmokeFailureFromOutput returns the last GT_SMOKE step and a short output tail.
func ParseSmokeFailureFromOutput(output string) SmokeFailureDetail {
	var d SmokeFailureDetail
	matches := smokeStepMarkerRE.FindAllStringSubmatch(output, -1)
	if len(matches) > 0 {
		d.FailedStep = strings.TrimSpace(matches[len(matches)-1][1])
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var tail []string
	for i := len(lines) - 1; i >= 0 && len(tail) < 12; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		tail = append([]string{line}, tail...)
	}
	d.RawTail = strings.TrimSpace(strings.Join(tail, "\n"))
	return d
}

// ImplementationVerifyNeedsRuntimeRework reports verify errors from HTTP smoke (not compile-only).
func ImplementationVerifyNeedsRuntimeRework(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "runtime smoke")
}

// implementationReworkPathsForSmoke lists handler, web, and server entry paths to reopen after smoke failure.
func implementationReworkPathsForSmoke(v WorkflowValidation) []string {
	seen := map[string]bool{}
	var out []string
	add := func(rel string) {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || seen[rel] {
			return
		}
		seen[rel] = true
		out = append(out, rel)
	}
	for _, rel := range implementPathsForRuntimeRework(v) {
		add(rel)
	}
	for _, rel := range append(append([]string(nil), v.RequiredFiles...), v.UnionRequiredFiles()...) {
		lower := strings.ToLower(filepath.ToSlash(strings.TrimSpace(rel)))
		if strings.HasSuffix(lower, "/cmd/server/main.go") {
			add(rel)
		}
	}
	sort.Strings(out)
	return out
}

// ReopenImplementationBeadsAfterSmokeFailure reopens closed handler/web/server beads when
// implementation phase verify fails on runtime smoke (disk may already look complete).
func ReopenImplementationBeadsAfterSmokeFailure(townRoot, rig string, v WorkflowValidation, verifyErr error) ([]string, error) {
	if rig == "" || townRoot == "" || verifyErr == nil {
		return nil, nil
	}
	if !ImplementationVerifyNeedsRuntimeRework(verifyErr) {
		return nil, nil
	}
	paths := implementationReworkPathsForSmokeDetail(townRoot, rig, v, verifyErr)
	if len(paths) == 0 {
		return reopenClosedImplementBeads(townRoot, rig, v)
	}
	return reopenClosedImplementBeadsForPaths(townRoot, rig, v, paths)
}

// implementationReworkPathsForSmokeDetail extends handler/web paths with the specific static
// asset named in a failed smoke GET probe (e.g. GET:/static/app.js → linkshelf/web/app.js).
func implementationReworkPathsForSmokeDetail(townRoot, rig string, v WorkflowValidation, verifyErr error) []string {
	seen := map[string]bool{}
	var out []string
	add := func(rel string) {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || seen[rel] {
			return
		}
		seen[rel] = true
		out = append(out, rel)
	}
	for _, rel := range implementationReworkPathsForSmoke(v) {
		add(rel)
	}
	if verifyErr != nil {
		detail := ParseSmokeFailureFromOutput(verifyErr.Error())
		if step := strings.TrimSpace(detail.FailedStep); strings.HasPrefix(step, "GET:") {
			urlPath := strings.TrimPrefix(step, "GET:")
			mapping := LoadWebStaticMappingFromRig(townRoot, rig, v)
			if webRel := WebFileFromStaticURL(urlPath, mapping, v.LayoutRoot); webRel != "" {
				add(webRel)
			}
		}
	}
	sort.Strings(out)
	return out
}

// FormatImplementationSmokeFailureBlock gives polecat guidance after smoke failure at success gate.
func FormatImplementationSmokeFailureBlock(townRoot, rig string, v WorkflowValidation, verifyErr error, reopened []string) string {
	if verifyErr == nil {
		return ""
	}
	rigDir := filepath.Join(townRoot, rig, "mayor", "rig")
	detail := ParseSmokeFailureFromOutput(verifyErr.Error())
	if detail.RawTail == "" {
		detail = ParseSmokeFailureFromOutput(extractLastSmokeFailure(verifyErr.Error()))
	}
	spec, _ := LoadAPISmokeSpecFromRig(townRoot, rig, v)
	mapping := LoadWebStaticMappingFromRig(townRoot, rig, v)
	missing := MissingWebAssetPaths(rigDir, v)

	var b strings.Builder
	b.WriteString("### Runtime smoke failed (implementation gate)\n")
	b.WriteString("Unit tests may pass while **runtime HTTP smoke** (doc-derived curls) still fails. Do not send JSON success until phase verify is green.\n\n")
	if detail.FailedStep != "" {
		b.WriteString("**Failed probe:** `")
		b.WriteString(smokeStepHumanLabel(detail.FailedStep))
		b.WriteString("`\n")
	}
	if len(reopened) > 0 {
		b.WriteString("\n**Reopened implement beads:** ")
		b.WriteString(strings.Join(reopened, ", "))
		b.WriteString(" — run `bd list --status=open`, then **Next bead** (bd update → verify → bd close).\n")
	} else {
		b.WriteString("\nRun `bd list --status=open`. If none, reopen the bead for handlers/web/server: `bd update <id> --status=open`.\n")
	}
	if len(missing) > 0 {
		b.WriteString("\n**Missing web files:** ")
		b.WriteString(strings.Join(missing, ", "))
		b.WriteString("\n")
	} else if step := detail.FailedStep; strings.HasPrefix(step, "GET:") {
		path := strings.TrimPrefix(step, "GET:")
		b.WriteString(smokeGETPathFixHint(path, mapping, spec.Port))
	}
	if detail.RawTail != "" {
		b.WriteString("\n**Smoke output (tail):**\n```\n")
		b.WriteString(truncateWorkflowText(detail.RawTail, 2000))
		b.WriteString("\n```\n")
	}
	b.WriteString("\n**Typical fixes:** serve `web/index.html` at `/`; static URLs from architecture (")
	if mapping.StaticURLPrefix != "" {
		b.WriteString(mapping.StaticURLPrefix)
		b.WriteString("/{file} → web/{file}")
	} else {
		b.WriteString("often /static/{file} → web/{file}")
	}
	b.WriteString("); register API routes from SPEC before closing handler beads.\n")
	return strings.TrimSpace(b.String())
}

func smokeStepHumanLabel(step string) string {
	switch step {
	case "wait_root":
		return "GET / (server did not respond on root)"
	case "GET:/":
		return "GET /"
	default:
		if strings.HasPrefix(step, "GET:") {
			return "GET " + strings.TrimPrefix(step, "GET:")
		}
		if strings.HasPrefix(step, "POST:") {
			return "POST " + strings.TrimPrefix(step, "POST:")
		}
		return step
	}
}

func smokeGETPathFixHint(urlPath string, mapping WebStaticMapping, port int) string {
	urlPath = normalizeSmokePath(urlPath)
	if urlPath == "" || urlPath == "/" {
		return "\n**Fix:** ensure `go run ./cmd/server` serves index from `web/index.html` at `/`.\n"
	}
	var b strings.Builder
	b.WriteString("\n**Fix for `")
	b.WriteString(urlPath)
	b.WriteString("`:** ")
	if mapping.StaticURLPrefix != "" && strings.HasPrefix(urlPath, mapping.StaticURLPrefix+"/") {
		rest := strings.TrimPrefix(urlPath, mapping.StaticURLPrefix)
		rest = strings.TrimPrefix(rest, "/")
		b.WriteString(fmt.Sprintf("map %s to `web/%s` in handlers (module cwd = layout root).\n", urlPath, rest))
	} else {
		b.WriteString("align handler static routes with architecture; files live under `web/`.\n")
	}
	if port > 0 {
		b.WriteString(fmt.Sprintf("(smoke uses port %d from docs.)\n", port))
	}
	return b.String()
}

// HandleImplementationPhaseVerifyFailure runs phase verify and, on runtime smoke failure,
// reopens handler/web/server beads and returns an error with actionable feedback.
func HandleImplementationPhaseVerifyFailure(townRoot, rig string, v WorkflowValidation) error {
	err := ImplementationPhaseVerifyOK(townRoot, rig, v)
	if err == nil {
		return nil
	}
	var reopened []string
	if ImplementationVerifyNeedsRuntimeRework(err) {
		reopened, _ = ReopenImplementationBeadsAfterSmokeFailure(townRoot, rig, v, err)
	}
	block := FormatImplementationSmokeFailureBlock(townRoot, rig, v, err, reopened)
	if block == "" {
		return err
	}
	return fmt.Errorf("%w\n\n%s", err, block)
}

// EnsureImplementSmokeReadyLog runs EnsureImplementBeadsAvailable for pre_run (smoke/verify reopen).
func EnsureImplementSmokeReadyLog(townRoot, rig string, v WorkflowValidation) (string, error) {
	reopened, err := EnsureImplementBeadsAvailable(townRoot, rig, v)
	if err != nil {
		return "", err
	}
	if len(reopened) == 0 {
		return "", nil
	}
	return "reopened implement beads after verify/smoke: " + joinStrings(reopened, ", "), nil
}
