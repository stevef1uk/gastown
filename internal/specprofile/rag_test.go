package specprofile

import (
	"testing"
)

func TestChunkDocument(t *testing.T) {
	text := `## Heading 1
Content for section 1.
More content.

### Subheading 1.1
Sub content.

## Heading 2
Content for section 2.
`

	chunks := ChunkDocument(text, "test.md")
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if chunks[0].Heading != "Heading 1" {
		t.Errorf("chunk 0 heading: %q", chunks[0].Heading)
	}
	if !contains(chunks[0].Content, "Content for section 1") {
		t.Errorf("chunk 0 missing content")
	}
	if chunks[1].Heading != "Subheading 1.1" {
		t.Errorf("chunk 1 heading: %q", chunks[1].Heading)
	}
	if chunks[2].Heading != "Heading 2" {
		t.Errorf("chunk 2 heading: %q", chunks[2].Heading)
	}
}

func TestExtractWords(t *testing.T) {
	words := extractWords("User authentication with JWT tokens")
	if words["authentication"] != 1 {
		t.Errorf("missing authentication")
	}
	if words["jwt"] != 1 {
		t.Errorf("missing jwt")
	}
	if words["tokens"] != 1 {
		t.Errorf("missing tokens")
	}
	if _, ok := words["with"]; ok {
		t.Errorf("stopword 'with' should be filtered")
	}
}

func TestScoreChunk(t *testing.T) {
	chunk := Chunk{
		Heading: "User Authentication and JWT",
		Content: "Users authenticate with JWT tokens. The auth handler validates tokens.",
	}
	focusWords := extractWords("auth core db")
	score := scoreChunk(chunk, focusWords)
	if score == 0 {
		t.Errorf("expected positive score for auth-related chunk")
	}

	unrelated := Chunk{
		Heading: "Frontend Theme Toggle",
		Content: "The theme toggle switches between light and dark mode.",
	}
	score2 := scoreChunk(unrelated, focusWords)
	if score2 >= score {
		t.Errorf("unrelated chunk should score lower than auth chunk")
	}
}

func TestExtractSpecExcerpts(t *testing.T) {
	specText := `## Auth Core DB
User authentication with JWT tokens. The auth handler validates tokens.
Database migrations for users table.

## Workspace Pages
Pages are hierarchical. Sidebar shows page tree.

## Block Engine
Blocks include paragraph, heading, todo. Drag and drop reordering.
`

	reqText := `### Pages and the sidebar
The sidebar shows every page as an expandable tree. Pages nest inside pages.

### The editor
A page is a stack of blocks, edited in place. Block types: paragraph, heading, todo.
`

	phases := []judgePhasePayload{
		{ID: "auth-core-db", SpecFocus: "User registration, login, JWT middleware"},
		{ID: "workspace-page", SpecFocus: "Workspace and hierarchical page CRUD"},
		{ID: "block-engine-1", SpecFocus: "Block CRUD, ordering, nested toggles"},
	}

	result := ExtractSpecExcerpts(phases, specText, reqText)

	if _, ok := result["auth-core-db"]; !ok {
		t.Errorf("auth-core-db missing from result")
	}
	if _, ok := result["workspace-page"]; !ok {
		t.Errorf("workspace-page missing from result")
	}
	if _, ok := result["block-engine-1"]; !ok {
		t.Errorf("block-engine-1 missing from result")
	}

	// Check that excerpts contain relevant keywords
	if !contains(result["auth-core-db"], "JWT") && !contains(result["auth-core-db"], "auth") {
		t.Errorf("auth excerpt missing auth/JWT: %q", result["auth-core-db"])
	}
	if !contains(result["workspace-page"], "page") && !contains(result["workspace-page"], "sidebar") {
		t.Errorf("workspace excerpt missing page/sidebar: %q", result["workspace-page"])
	}
	if !contains(result["block-engine-1"], "block") {
		t.Errorf("block excerpt missing block: %q", result["block-engine-1"])
	}
}

func TestExtractSpecExcerptsEmptyFocus(t *testing.T) {
	specText := "## Heading\nContent"
	phases := []judgePhasePayload{
		{ID: "phase1", SpecFocus: "", Title: "Fallback Title"},
	}
	result := ExtractSpecExcerpts(phases, specText, "")
	if len(result) == 0 {
		t.Log("empty focus returns no excerpts (acceptable)")
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 1; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}