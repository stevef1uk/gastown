package orchestrator

import (
	"fmt"
	"strings"
)

// RigFlowQARuntimeSmokeBlock is injected into qa_review.md from the rig profile and SPEC.
func RigFlowQARuntimeSmokeBlock(townRoot, rig string, v WorkflowValidation) string {
	if WorkflowUsesPython(v) {
		return rigFlowQAPythonVerifyBlock(v)
	}
	if !WorkflowUsesGo(v) {
		return rigFlowQAGenericVerifyBlock(v)
	}
	if !workflowHasGoWebAndServer(v) {
		return rigFlowQAGoLibraryVerifyBlock(v)
	}
	spec, _ := LoadAPISmokeSpecFromRig(townRoot, rig, v)
	if APISmokeHasHTTPAPI(spec) {
		return rigFlowQAGoWebAPISmokeBlock(v, spec)
	}
	if len(spec.StaticAssets) > 0 || smokeHasNonRootGET(spec) {
		return rigFlowQAGoWebStaticSmokeBlock(v)
	}
	return rigFlowQAGoLibraryVerifyBlock(v)
}

func rigFlowQAGenericVerifyBlock(v WorkflowValidation) string {
	return fmt.Sprintf(`## Runtime verification

Run profile verification only: %s

Do **not** invent HTTP smoke unless SPEC.md documents a server and HTTP table.`, v.UnittestCommandHint())
}

func rigFlowQAGoLibraryVerifyBlock(v WorkflowValidation) string {
	return fmt.Sprintf(`## Runtime verification (no web server in profile)

This phase has **no** `+"`cmd/server/main.go`"+` + `+"`web/`"+` pair — **skip** `+"`go run`"+` and **curl**.

Run: %s`, v.UnittestCommandHint())
}

func rigFlowQAPythonVerifyBlock(v WorkflowValidation) string {
	venv := v.PythonVenvRelDir()
	block := fmt.Sprintf(`## Verification (Python rig — layout %s)

| Do | Do not |
|----|--------|
| Run %s from mayor/rig`, v.LayoutRootDir(), v.UnittestCommandHint())
	if venv != "" {
		block += fmt.Sprintf(" (venv `%s/` under mayor/rig)", venv)
	}
	block += " | " + "`go test`" + ", " + "`go run`" + ", " + "`go mod`" + ", or Go-style curl smoke |\n"
	block += "| Read SPEC for **backend/** or **src/** paths — not assumed `cmd/server` + `web/` | Copy linkshelf/Go layout unless architecture says so |\n"
	block += "| HTTP checks **only** if SPEC.md has an HTTP/API table | curl or `go run` when SPEC has no server |\n\n"
	if v.RequirementsFilePath() != "" {
		block += fmt.Sprintf("Install deps once if needed: `test -f %q && python3 -m pip install -r %q` (into `%s/`).\n\n", v.RequirementsFilePath(), v.RequirementsFilePath(), venv)
	}
	block += "If SPEC defines no REST/HTTP endpoints, **pytest/unittest alone** is sufficient for `all_passed`.\n"
	return block
}

func rigFlowQAGoWebStaticSmokeBlock(v WorkflowValidation) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	return fmt.Sprintf(`## Runtime smoke (static web — **no HTTP API** in SPEC)

SPEC/architecture define **no** JSON API routes to curl. gt-agent probes **GET /** and static assets from **index.html** only.

One CMD (gt-agent injects curls — do not hand-pick paths):

`+"```"+`
CMD: cd {{rig}}/mayor/rig/%s && go run ./cmd/server
`+"```"+`

Do **not** require POST or JSON API paths unless SPEC adds them later. Use **failure** for 404 static assets; **architecture_failure** only when tests pass but documented static routing is wrong.

%s`, layout, RigFlowStaticURLContractGuidance)
}

func rigFlowQAGoWebAPISmokeBlock(v WorkflowValidation, spec APISmokeSpec) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	var paths []string
	for _, p := range spec.GETPaths {
		if p != "" {
			paths = append(paths, p)
		}
	}
	for _, post := range spec.POSTProbes {
		if post.Path != "" {
			paths = append(paths, "POST "+post.Path)
		}
	}
	routeNote := "routes from SPEC/architecture"
	if len(paths) > 0 {
		routeNote = strings.Join(paths, ", ")
	}
	return fmt.Sprintf(`## Web/API runtime smoke (from SPEC — %s)

Unit tests alone miss integration bugs. One CMD starting the server (gt-agent **frees port 8080** and stops the server when the step finishes):

`+"```"+`
CMD: cd {{rig}}/mayor/rig/%s && go run ./cmd/server
`+"```"+`

| Check | How |
|-------|-----|
| Static assets | curl -sf each path from index.html (see architecture) |
| Empty API list | GET list endpoints must return JSON **[]** not **null** |
| Create API | POST endpoints from SPEC must not **405** |

%s

gt-agent rewrites `+"`go run ./cmd/server`"+` into background server + curls for **only** paths in SPEC/architecture/plan — not invented API routes.

If smoke fails, next message **JSON only** with HTTP status and bead IDs — do not repeat long smoke CMDs.`, routeNote, layout, RigFlowStaticURLContractGuidance)
}
