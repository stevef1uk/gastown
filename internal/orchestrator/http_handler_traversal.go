package orchestrator

// GoTestOutputSuggestsTraversalRedirect reports httptest failures where path traversal
// got a redirect (often 307) instead of 404 — matchers come from http-implementation profile JSON.
func GoTestOutputSuggestsTraversalRedirect(townRoot, rig string, v WorkflowValidation, cmdOutput string) bool {
	return LoadHTTPImplementationProfile(townRoot, rig, v).GoTestOutputSuggestsTraversalRedirect(cmdOutput)
}

// FormatHandlerTraversalRedirectHint explains how to satisfy traversal table tests (from profile JSON).
func FormatHandlerTraversalRedirectHint(townRoot, rig, beadPath string, v WorkflowValidation) string {
	return LoadHTTPImplementationProfile(townRoot, rig, v).FormatTraversalRedirectHint(beadPath, v)
}

// HandlerStaticServePatternIssues returns write-time problems in handlers.go (from profile JSON).
func HandlerStaticServePatternIssues(townRoot, rig string, body string, v WorkflowValidation) []string {
	return LoadHTTPImplementationProfile(townRoot, rig, v).HandlerWriteGuardIssues(body)
}
