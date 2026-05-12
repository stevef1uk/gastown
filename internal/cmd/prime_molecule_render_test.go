package cmd

import (
	"strings"
	"testing"
)

// TestSanitizeStepBody_StripsPlaceholderBlocks is the core regression
// test for fix #85: the polecat's LLM was emitting CMD lines verbatim
// from formula example bash like
//   git branch -a | grep <branch-from-notes>
// because the focused step body inlined those examples. The sanitizer
// must drop fenced blocks whose content contains <placeholder> markers
// or {{template_vars}} that didn't get substituted.
func TestSanitizeStepBody_StripsPlaceholderBlocks(t *testing.T) {
	in := "Prose before.\n" +
		"```bash\n" +
		"git branch -a | grep <branch-from-notes>\n" +
		"# If it exists: git checkout <branch> && git rebase origin/main\n" +
		"```\n" +
		"More prose."
	out := sanitizeStepBody(in)

	if strings.Contains(out, "<branch-from-notes>") {
		t.Fatalf("placeholder still present after sanitize:\n%s", out)
	}
	if strings.Contains(out, "git branch -a | grep") {
		t.Fatalf("placeholder-laden command body should have been removed:\n%s", out)
	}
	if !strings.Contains(out, "example block omitted") {
		t.Fatalf("expected omission marker in sanitized output:\n%s", out)
	}
	if !strings.Contains(out, "Prose before.") || !strings.Contains(out, "More prose.") {
		t.Fatalf("surrounding prose must be preserved:\n%s", out)
	}
}

// TestSanitizeStepBody_KeepsCleanBlocks verifies the sanitizer does NOT
// strip code blocks whose commands are concrete and runnable.
func TestSanitizeStepBody_KeepsCleanBlocks(t *testing.T) {
	in := "Prime first:\n" +
		"```bash\n" +
		"gt prime\n" +
		"bd prime\n" +
		"```\n" +
		"Then inspect."
	out := sanitizeStepBody(in)

	if !strings.Contains(out, "gt prime") || !strings.Contains(out, "bd prime") {
		t.Fatalf("clean code block must be preserved verbatim:\n%s", out)
	}
	if strings.Contains(out, "example block omitted") {
		t.Fatalf("clean code block must not be stripped:\n%s", out)
	}
}

// TestSanitizeStepBody_StripsUnresolvedTemplateVars catches the
// orthogonal hazard: {{issue}} and friends that weren't substituted.
// The renderer's varMap fills these in when possible, but if a formula
// declares a var with no default and the caller didn't pass one, the
// raw `{{varname}}` leaks through. Those code blocks are unsafe to
// expose to a small LLM.
func TestSanitizeStepBody_StripsUnresolvedTemplateVars(t *testing.T) {
	in := "Read the issue:\n" +
		"```bash\n" +
		"bd show {{issue}}\n" +
		"```\n"
	out := sanitizeStepBody(in)

	if strings.Contains(out, "{{issue}}") {
		t.Fatalf("unresolved template var must be stripped:\n%s", out)
	}
}

// TestSanitizeStepBody_PreservesShellRedirection is a guardrail: shell
// `<` redirections and `<<EOF` heredocs are NOT placeholders. The
// sanitizer regex should leave them alone.
func TestSanitizeStepBody_PreservesShellRedirection(t *testing.T) {
	in := "Heredoc example:\n" +
		"```bash\n" +
		"cat > /tmp/file.txt <<'EOF'\n" +
		"hello world\n" +
		"EOF\n" +
		"```\n"
	out := sanitizeStepBody(in)

	if !strings.Contains(out, "cat > /tmp/file.txt") {
		t.Fatalf("legitimate redirection block must be preserved:\n%s", out)
	}
	if strings.Contains(out, "example block omitted") {
		t.Fatalf("heredoc block should not be classified as placeholder:\n%s", out)
	}
}

// TestSanitizeStepBody_MultipleBlocksMixed verifies the sanitizer
// correctly handles a body with both clean and placeholder-laden
// blocks: keep the clean ones, drop the bad ones.
func TestSanitizeStepBody_MultipleBlocksMixed(t *testing.T) {
	in := "Step 1 prime:\n" +
		"```bash\n" +
		"gt prime\n" +
		"```\n" +
		"Step 2 (conditional):\n" +
		"```bash\n" +
		"git checkout <branch>\n" +
		"```\n" +
		"Final note."
	out := sanitizeStepBody(in)

	if !strings.Contains(out, "gt prime") {
		t.Fatalf("clean block lost:\n%s", out)
	}
	if strings.Contains(out, "git checkout <branch>") {
		t.Fatalf("placeholder block retained:\n%s", out)
	}
	if !strings.Contains(out, "Final note.") {
		t.Fatalf("trailing prose lost:\n%s", out)
	}
}


// TestShowFormulaStepsFull_CompactByDefault verifies that the default
// compact view of `mol-polecat-work` is dramatically smaller than the
// legacy full dump (which is ~20KB and was confusing small LLMs into
// regurgitating template fragments). See sling fix #82 / formula
// slim-down work for context.
func TestShowFormulaStepsFull_CompactByDefault(t *testing.T) {
	SetFormulaStepFocus(0)
	defer SetFormulaStepFocus(0)
	out := captureStdout(t, func() {
		_ = showFormulaStepsFull("mol-polecat-work", "", "")
	})

	if !strings.Contains(out, "Formula Checklist") {
		t.Fatalf("missing checklist header: %s", out)
	}
	if !strings.Contains(out, "► Step 1") {
		t.Fatalf("missing focus marker for step 1: %s", out)
	}
	if !strings.Contains(out, "How to work this checklist") {
		t.Fatalf("missing usage hint: %s", out)
	}

	// The full dump is ~20,000 chars. The compact view should be
	// substantially smaller — well under 5KB on this formula.
	t.Logf("mol-polecat-work compact view size: %d bytes (was ~20,000 in full mode)", len(out))
	if len(out) > 6000 {
		t.Fatalf("compact view is too large: %d bytes (want < 6000)", len(out))
	}
	if len(out) < 800 {
		t.Fatalf("compact view is suspiciously small: %d bytes", len(out))
	}
}

// TestShowFormulaStepsFull_FocusFlag verifies that --step N pins the
// renderer to a single step's full body.
func TestShowFormulaStepsFull_FocusFlag(t *testing.T) {
	SetFormulaStepFocus(3)
	defer SetFormulaStepFocus(0)

	out := captureStdout(t, func() {
		_ = showFormulaStepsFull("mol-polecat-work", "", "")
	})

	if !strings.Contains(out, "► Step 3 (your current step)") {
		t.Fatalf("expected focused step 3 in output: %s", clampForSnippet(out, 500))
	}
	// Step 1 body should NOT be the focused one when we asked for step 3.
	if strings.Contains(out, "► Step 1 (your current step)") {
		t.Fatalf("step 1 should not be focused when --step 3 was requested")
	}
}

// TestShowFormulaStepsFull_LegacyFullMode verifies that
// GT_FORMULA_VIEW=full re-enables the legacy verbose dump for callers
// (e.g. crew workers backed by large models) that still want it.
func TestShowFormulaStepsFull_LegacyFullMode(t *testing.T) {
	t.Setenv("GT_FORMULA_VIEW", "full")
	SetFormulaStepFocus(0)
	defer SetFormulaStepFocus(0)

	out := captureStdout(t, func() {
		_ = showFormulaStepsFull("mol-polecat-work", "", "")
	})

	// In full mode we expect the renderer to print the legacy
	// `### Step N:` heading for every step.
	for _, want := range []string{"### Step 1:", "### Step 2:", "### Step 3:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("legacy full mode missing %q", want)
		}
	}
	// Full mode does NOT print the "How to work this checklist" footer.
	if strings.Contains(out, "How to work this checklist") {
		t.Fatalf("legacy full mode should not print compact usage hint")
	}
}

func clampForSnippet(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}
