package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
)

// RigFlowQARuntimeSmokeBlock is injected into qa_review.md from the rig profile and SPEC.
func RigFlowQARuntimeSmokeBlock(townRoot, rig string, v WorkflowValidation) string {
	v = v.ForActivePhase()
	mayorRigDir := filepath.Join(townRoot, rig, "mayor", "rig")

	var guidance []string
	if report, err := LoadAPIContractFromRig(mayorRigDir); err == nil && !report.IsClean() {
		guidance = append(guidance, FormatAPIContractGuidance(report))
	}
	if report := DetectStaticExportAndServing(mayorRigDir); !report.IsClean() {
		guidance = append(guidance, FormatStaticExportGuidance(report))
	}
	if report := DetectTestComposeIssues(mayorRigDir); !report.IsClean() {
		guidance = append(guidance, FormatTestComposeGuidance(report))
	}

	base := ""
	switch {
	case WorkflowUsesPython(v):
		if WorkflowNeedsQARuntimeSmoke(townRoot, rig, v) {
			spec, _ := LoadAPISmokeSpecFromRig(townRoot, rig, v)
			base = rigFlowQAPythonWebAPISmokeBlock(v, spec)
		} else {
			base = rigFlowQAPythonVerifyBlock(v)
		}
	case !WorkflowUsesGo(v):
		base = rigFlowQAGenericVerifyBlock(v)
	case !WorkflowNeedsQARuntimeSmoke(townRoot, rig, v):
		base = rigFlowQAGoLibraryVerifyBlock(v)
	default:
		spec, _ := LoadAPISmokeSpecFromRig(townRoot, rig, v)
		if APISmokeHasHTTPAPI(spec) {
			base = rigFlowQAGoWebAPISmokeBlock(v, spec)
		} else if len(spec.StaticAssets) > 0 || smokeHasNonRootGETProbes(spec) {
			base = rigFlowQAGoWebStaticSmokeBlock(v)
		} else {
			base = rigFlowQAGoLibraryVerifyBlock(v)
		}
	}

	if e2e := rigFlowE2ESmokeBlock(v, mayorRigDir); e2e != "" {
		guidance = append(guidance, e2e)
	}

	if len(guidance) == 0 {
		return base
	}
	return base + "\n\n" + strings.Join(guidance, "\n\n")
}

func rigFlowQAGenericVerifyBlock(v WorkflowValidation) string {
	return fmt.Sprintf(`## Runtime verification

Run profile verification only: %s

Do **not** invent HTTP smoke unless SPEC.md documents a server and HTTP table.`, v.QAVerifyHint())
}

func rigFlowQAGoLibraryVerifyBlock(v WorkflowValidation) string {
	return fmt.Sprintf(`## Runtime verification (no web server in profile)

This phase has **no** `+"`cmd/server/main.go`"+` + `+"`web/`"+` pair, so **skip** runtime smoke. **Do NOT output `+"`go run`"+` or `+"`curl`"+` commands — the agent WILL reject them and you will waste turns in a loop.**

Only run: %s

Return JSON outcome after that — no other CMD lines needed.`, v.QAVerifyHint())
}

func rigFlowQAPythonVerifyBlock(v WorkflowValidation) string {
	venv := v.PythonVenvRelDir()
	block := fmt.Sprintf(`## Verification (Python rig — layout %s)

| Do | Do not |
|----|--------|
| Run %s from mayor/rig`, v.LayoutRootDir(), v.QAVerifyHint())
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

func rigFlowQAPythonWebAPISmokeBlock(v WorkflowValidation, spec APISmokeSpec) string {
	layout := strings.Trim(strings.TrimSpace(v.LayoutRoot), "/")
	if layout == "" {
		layout = "."
	}
	var paths []string
	for _, p := range spec.Probes {
		if p.Source == "static" {
			continue
		}
		path := normalizeSmokePath(p.Path)
		if path == "" {
			continue
		}
		method := strings.ToUpper(strings.TrimSpace(p.Method))
		if method == "POST" {
			paths = append(paths, "POST "+path)
		} else if path != "/" {
			paths = append(paths, path)
		}
	}
	routeNote := "routes from SPEC/architecture"
	if len(paths) > 0 {
		routeNote = strings.Join(paths, ", ")
	}
	return fmt.Sprintf(`## Web/API runtime smoke (Python — %s)

Run **pytest** from layout, then one server CMD. gt-agent rewrites uvicorn/gunicorn/flask into background server + **curl for each documented route** (same HTTP table as implementation).

Document the server under **## Runtime smoke server** in SPEC/architecture, or put uvicorn/gunicorn in qa_verify_command, e.g.:

`+"```"+`
## Runtime smoke server
.venv/bin/python3 -m uvicorn %s.app:app --host 127.0.0.1 --port 8080
`+"```"+`

`+"```"+`
CMD: cd {{rig}}/mayor/rig/%s && .venv/bin/python3 -m uvicorn %s.app:app --host 127.0.0.1 --port 8080
`+"```"+`

| Check | How |
|-------|-----|
| Unit tests | %s |
| HTTP probes | gt-agent curls **only** paths from SPEC (GET/POST table) |
| Fresh state | **## Runtime smoke reset** or persistence paths in architecture |

%s`, routeNote, layout, layout, layout, v.QAVerifyHint(), RigFlowStaticURLContractGuidance)
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
	for _, p := range spec.Probes {
		if p.Source == "static" {
			continue
		}
		path := normalizeSmokePath(p.Path)
		if path == "" {
			continue
		}
		method := strings.ToUpper(strings.TrimSpace(p.Method))
		if method == "POST" {
			paths = append(paths, "POST "+path)
		} else if path != "/" {
			paths = append(paths, path)
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
| Fresh state | Document persistence under **## Runtime smoke reset** (or name DB paths in architecture); gt-agent removes those files before smoke when empty-array GETs are defined |

%s

gt-agent rewrites `+"`go run ./cmd/server`"+` into background server + curls for **only** paths in SPEC/architecture/plan — not invented API routes. Persistence files listed in docs are deleted before the probe so prior QA runs do not leave rows that fail empty-array checks.

If smoke fails, next message **JSON only** with HTTP status and bead IDs — do not repeat long smoke CMDs.`, routeNote, layout, RigFlowStaticURLContractGuidance)
}

func rigFlowE2ESmokeBlock(v WorkflowValidation, mayorRigDir string) string {
	scoped := v.ForActivePhase()
	var e2eFiles []string
	for _, f := range scoped.RequiredFiles {
		if IsE2ETestPath(f) {
			e2eFiles = append(e2eFiles, f)
		}
	}
	if len(e2eFiles) == 0 {
		return ""
	}
	hasCompose := false
	composeFile := ""
	cli := DockerComposeCLI()
	for _, f := range scoped.RequiredFiles {
		base := strings.ToLower(filepath.Base(f))
		if strings.HasPrefix(base, "docker-compose") {
			hasCompose = true
			break
		}
	}
	if hasCompose {
		composeFile = dockerComposeFileForPhase(scoped.RequiredFiles)
	}
	var b strings.Builder
	b.WriteString("## E2E / browser smoke test (exercises real UI and API)\n\n")
	b.WriteString("The active phase includes E2E test files in `required_files`. Before returning `all_passed`, **run the E2E tests** so they exercise the real UI and API in a browser — curl alone is not enough.\n\n")
	if hasCompose && composeFile != "" {
		b.WriteString("The rig's compose uses the **locally-built shared runner image** `playwright-go-test:latest` (built asynchronously at workflow start from `mcr.microsoft.com/playwright:v1.62.1-jammy`). Do **not** require a specific public `mcr.microsoft.com/playwright:<version>` tag — the local image is canonical and needs no registry pull. For host-run rigs the container reaches the host-launched server via `host.docker.internal` (compose `extra_hosts` host-gateway); do **not** require `network_mode: host` or a `127.0.0.1` base URL — host networking does not expose the macOS host's loopback to containers.\n\n")
		b.WriteString("| Check | How |\n|-------|-----|\n")
		b.WriteString(fmt.Sprintf("| E2E tests pass | `cd {{rig}}/mayor/rig && %s -f %s down && %s -f %s up --exit-code-from playwright` |\n", cli, composeFile, cli, composeFile))
		b.WriteString("| Page loads | Tests use `page.goto` / `page.locator` against the running app |\n")
		b.WriteString("| API exercised | Tests call the real backend through the browser (fetch/XHR), not mocks |\n\n")
	} else {
		b.WriteString("| Check | How |\n|-------|-----|\n")
		b.WriteString("| E2E tests pass | `cd {{rig}}/mayor/rig && npx playwright test` (or the project's configured test runner) |\n")
		b.WriteString("| Page loads | Tests use `page.goto` / `page.locator` against the running app |\n")
		b.WriteString("| API exercised | Tests call the real backend through the browser (fetch/XHR), not mocks |\n\n")
	}
	b.WriteString("If E2E tests fail, return `failure` with the test output — do not advance the phase.\n")
	return b.String()
}
