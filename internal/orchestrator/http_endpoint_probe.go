package orchestrator

import (
	"fmt"
	"sort"
	"strings"
)

// SmokeExpect is how runtime smoke validates an HTTP response (doc-derived).
type SmokeExpect int

const (
	SmokeExpectOK SmokeExpect = iota
	SmokeExpectEmptyJSONArray
)

// HTTPEndpointProbe is one curl step against a documented route (SPEC/architecture/plan).
// The same HTTP tables feed architecture contract text and runtime smoke (no per-rig paths).
type HTTPEndpointProbe struct {
	Method string // GET, POST, …
	Path   string // root-relative, e.g. /api/links
	Expect SmokeExpect
	Body   string // POST JSON body when Method is POST
	Source string // api | static | root
}

func (p HTTPEndpointProbe) smokeMarkerLabel() string {
	return strings.ToUpper(strings.TrimSpace(p.Method)) + ":" + normalizeSmokePath(p.Path)
}

func (p HTTPEndpointProbe) shellSteps(base string) []string {
	method := strings.ToUpper(strings.TrimSpace(p.Method))
	path := normalizeSmokePath(p.Path)
	if path == "" {
		return nil
	}
	url := base + path
	var parts []string
	if m := smokeStepMarker(p.smokeMarkerLabel()); m != "" {
		parts = append(parts, m)
	}
	const curlOpts = `--connect-timeout 1 --max-time 2`
	switch method {
	case "GET":
		switch p.Expect {
		case SmokeExpectEmptyJSONArray:
			parts = append(parts, fmt.Sprintf(`test "$(curl -s %s %s)" = "[]"`, curlOpts, bashSingleQuote(url)))
		default:
			parts = append(parts, fmt.Sprintf(`curl -sf %s %s >/dev/null`, curlOpts, bashSingleQuote(url)))
		}
	case "POST":
		body := strings.ReplaceAll(p.Body, `'`, `'\''`)
		parts = append(parts, fmt.Sprintf(
			`curl -sf %s -X POST -H 'Content-Type: application/json' -d '%s' %s >/dev/null`,
			curlOpts, body, bashSingleQuote(url)))
	default:
		parts = append(parts, fmt.Sprintf(`curl -sf %s -X %s %s >/dev/null`, curlOpts, method, bashSingleQuote(url)))
	}
	return parts
}

// appendAPIProbe records a documented API route (deduped by method+path).
func appendAPIProbe(spec *APISmokeSpec, seen map[string]bool, method, path, detail, specText string) {
	if spec == nil {
		return
	}
	path = normalizeSmokePath(path)
	if path == "" || !strings.HasPrefix(path, "/") {
		return
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	key := method + " " + path
	if seen[key] {
		return
	}
	seen[key] = true
	probe := HTTPEndpointProbe{Method: method, Path: path, Source: "api"}
	if method == "GET" {
		detailLower := strings.ToLower(detail)
		if strings.Contains(detailLower, "json array") || strings.Contains(detailLower, "`[]`") ||
			strings.Contains(detailLower, "returns `[]`") || strings.Contains(detailLower, "not `null`") ||
			(strings.Contains(detailLower, "[]") && strings.Contains(detailLower, "empty")) {
			probe.Expect = SmokeExpectEmptyJSONArray
		}
	}
	if method == "POST" {
		if strings.Contains(path, "{") {
			return
		}
		probe.Body = defaultPOSTBodyFromSpec(specText)
	}
	spec.Probes = append(spec.Probes, probe)
}

func appendStaticProbes(spec *APISmokeSpec, assets []string) {
	if spec == nil {
		return
	}
	seen := map[string]bool{}
	for _, p := range spec.Probes {
		if strings.EqualFold(p.Method, "GET") {
			seen["GET "+normalizeSmokePath(p.Path)] = true
		}
	}
	for _, asset := range assets {
		path := normalizeSmokePath(asset)
		if path == "" {
			continue
		}
		key := "GET " + path
		if seen[key] {
			continue
		}
		seen[key] = true
		spec.Probes = append(spec.Probes, HTTPEndpointProbe{
			Method: "GET",
			Path:   path,
			Source: "static",
		})
	}
}

// materializeAPISmokeProbes builds Probes from legacy GETPaths/POSTProbes/StaticAssets when tests
// or callers construct APISmokeSpec without going through LoadAPISmokeSpecFromRig.
func materializeAPISmokeProbes(spec *APISmokeSpec) {
	if spec == nil || len(spec.Probes) > 0 {
		return
	}
	emptySet := map[string]bool{}
	for _, p := range spec.GETEmptyJSONArray {
		emptySet[normalizeSmokePath(p)] = true
	}
	seen := map[string]bool{}
	add := func(p HTTPEndpointProbe) {
		key := strings.ToUpper(strings.TrimSpace(p.Method)) + " " + normalizeSmokePath(p.Path)
		if seen[key] {
			return
		}
		seen[key] = true
		spec.Probes = append(spec.Probes, p)
	}
	for _, path := range spec.GETPaths {
		path = normalizeSmokePath(path)
		if path == "" {
			continue
		}
		exp := SmokeExpectOK
		if emptySet[path] {
			exp = SmokeExpectEmptyJSONArray
		}
		src := "api"
		for _, s := range spec.StaticAssets {
			if normalizeSmokePath(s) == path {
				src = "static"
				break
			}
		}
		add(HTTPEndpointProbe{Method: "GET", Path: path, Expect: exp, Source: src})
	}
	for _, path := range spec.StaticAssets {
		path = normalizeSmokePath(path)
		if path == "" {
			continue
		}
		add(HTTPEndpointProbe{Method: "GET", Path: path, Source: "static"})
	}
	for _, post := range spec.POSTProbes {
		add(HTTPEndpointProbe{Method: "POST", Path: post.Path, Body: post.Body, Source: "api"})
	}
}

// syncAPISmokeSpecDerivedFields rebuilds legacy slice fields from Probes for callers not yet migrated.
func syncAPISmokeSpecDerivedFields(spec *APISmokeSpec) {
	if spec == nil {
		return
	}
	emptySet := map[string]bool{}
	var getPaths, staticAssets, emptyJSONArray []string
	var postProbes []POSTSmokeProbe
	getSeen := map[string]bool{}
	staticSeen := map[string]bool{}
	postSeen := map[string]bool{}
	for _, p := range spec.Probes {
		path := normalizeSmokePath(p.Path)
		if path == "" {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(p.Method)) {
		case "GET":
			if !getSeen[path] {
				getSeen[path] = true
				getPaths = append(getPaths, path)
			}
			if p.Expect == SmokeExpectEmptyJSONArray {
				emptySet[path] = true
			}
			if p.Source == "static" && !staticSeen[path] {
				staticSeen[path] = true
				staticAssets = append(staticAssets, path)
			}
		case "POST":
			if postSeen[path] || strings.Contains(path, "{") {
				continue
			}
			postSeen[path] = true
			postProbes = append(postProbes, POSTSmokeProbe{Path: path, Body: p.Body})
		}
	}
	for p := range emptySet {
		emptyJSONArray = append(emptyJSONArray, p)
	}
	sort.Strings(getPaths)
	sort.Strings(emptyJSONArray)
	sort.Strings(staticAssets)
	sort.Slice(postProbes, func(i, j int) bool { return postProbes[i].Path < postProbes[j].Path })
	spec.GETPaths = getPaths
	spec.GETEmptyJSONArray = emptyJSONArray
	spec.StaticAssets = staticAssets
	spec.POSTProbes = postProbes
}

// orderedSmokeProbes returns curl order: static assets, then API GET (except /), then POST.
func (spec APISmokeSpec) orderedSmokeProbes() []HTTPEndpointProbe {
	materializeAPISmokeProbes(&spec)
	var statics, apiGETs, posts []HTTPEndpointProbe
	for _, p := range spec.Probes {
		method := strings.ToUpper(strings.TrimSpace(p.Method))
		path := normalizeSmokePath(p.Path)
		switch method {
		case "GET":
			if path == "/" {
				continue
			}
			if p.Source == "static" {
				statics = append(statics, p)
			} else {
				apiGETs = append(apiGETs, p)
			}
		case "POST":
			posts = append(posts, p)
		}
	}
	sort.Slice(statics, func(i, j int) bool { return statics[i].Path < statics[j].Path })
	sort.Slice(apiGETs, func(i, j int) bool { return apiGETs[i].Path < apiGETs[j].Path })
	sort.Slice(posts, func(i, j int) bool { return posts[i].Path < posts[j].Path })
	out := make([]HTTPEndpointProbe, 0, len(statics)+len(apiGETs)+len(posts))
	out = append(out, statics...)
	out = append(out, apiGETs...)
	out = append(out, posts...)
	return out
}

func (spec APISmokeSpec) hasRootGET() bool {
	materializeAPISmokeProbes(&spec)
	for _, p := range spec.Probes {
		if strings.EqualFold(p.Method, "GET") && normalizeSmokePath(p.Path) == "/" {
			return true
		}
	}
	return false
}

// APISmokeHasHTTPAPI reports documented JSON/API routes (not static asset GETs).
func APISmokeHasHTTPAPI(spec APISmokeSpec) bool {
	materializeAPISmokeProbes(&spec)
	for _, p := range spec.Probes {
		if p.Source != "api" {
			continue
		}
		if strings.EqualFold(p.Method, "POST") && smokeProbeAPIPath(p.Path) {
			return true
		}
		if strings.EqualFold(p.Method, "GET") && smokeProbeAPIPath(p.Path) {
			return true
		}
	}
	return false
}

func smokeHasNonRootGETProbes(spec APISmokeSpec) bool {
	materializeAPISmokeProbes(&spec)
	for _, p := range spec.Probes {
		if !strings.EqualFold(p.Method, "GET") {
			continue
		}
		if path := normalizeSmokePath(p.Path); path != "" && path != "/" {
			return true
		}
	}
	return false
}
