package main

import "testing"

func TestFSM_parseDSML_deepseekV4_singleRead(t *testing.T) {
	t.Parallel()
	input := "<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"read\"\n｜DSML｜parameter name=\"file_path\" string=\"true\">finally/mayor/rig/frontend/package.json</｜DSML｜parameter>\n/｜DSML｜invoke\n</｜DSML｜tool_calls>"
	got := parseDSML(input)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d: %v", len(got), got)
	}
	if got[0].Tool != "READ" || got[0].Content != "finally/mayor/rig/frontend/package.json" {
		t.Errorf("got[0] = %v", got[0])
	}
}

func TestFSM_parseDSML_deepseekV4_bash(t *testing.T) {
	t.Parallel()
	input := "<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"bash\"\n｜DSML｜parameter name=\"command\" string=\"true\">go test ./...</｜DSML｜parameter>\n/｜DSML｜invoke\n</｜DSML｜tool_calls>"
	got := parseDSML(input)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d: %v", len(got), got)
	}
	if got[0].Tool != "CMD" || got[0].Content != "go test ./..." {
		t.Errorf("got[0] = %v", got[0])
	}
}

func TestFSM_parseDSML_deepseekV4_write(t *testing.T) {
	t.Parallel()
	input := "<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"write_file\"\n｜DSML｜parameter name=\"file_path\" string=\"true\">foo.go</｜DSML｜parameter>\n｜DSML｜parameter name=\"content\" string=\"true\">package main</｜DSML｜parameter>\n/｜DSML｜invoke\n</｜DSML｜tool_calls>"
	got := parseDSML(input)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d: %v", len(got), got)
	}
	if got[0].Tool != "WRITE" || got[0].Content != "foo.go" {
		t.Errorf("got[0] = %v", got[0])
	}
}

func TestFSM_parseDSML_deepseekV4_edit(t *testing.T) {
	t.Parallel()
	input := "<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"edit_file\"\n｜DSML｜parameter name=\"file_path\" string=\"true\">bar.go</｜DSML｜parameter>\n/｜DSML｜invoke\n</｜DSML｜tool_calls>"
	got := parseDSML(input)
	if len(got) != 1 {
		t.Fatalf("expected 1, got %d: %v", len(got), got)
	}
	if got[0].Tool != "EDIT" || got[0].Content != "bar.go" {
		t.Errorf("got[0] = %v", got[0])
	}
}

func TestFSM_parseDSML_deepseekV4_mixedTools(t *testing.T) {
	t.Parallel()
	input := "<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"read\"\n｜DSML｜parameter name=\"file_path\" string=\"true\">foo.go</｜DSML｜parameter>\n/｜DSML｜invoke\n<｜DSML｜invoke name=\"bash\"\n｜DSML｜parameter name=\"command\" string=\"true\">go test ./...</｜DSML｜parameter>\n/｜DSML｜invoke\n<｜DSML｜invoke name=\"write_file\"\n｜DSML｜parameter name=\"file_path\" string=\"true\">bar.go</｜DSML｜parameter>\n｜DSML｜parameter name=\"content\" string=\"true\">package main</｜DSML｜parameter>\n/｜DSML｜invoke\n</｜DSML｜tool_calls>"
	got := parseDSML(input)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d: %v", len(got), got)
	}
	if got[0].Tool != "READ" || got[0].Content != "foo.go" {
		t.Errorf("got[0] = %v", got[0])
	}
	if got[1].Tool != "CMD" || got[1].Content != "go test ./..." {
		t.Errorf("got[1] = %v", got[1])
	}
	if got[2].Tool != "WRITE" || got[2].Content != "bar.go" {
		t.Errorf("got[2] = %v", got[2])
	}
}

func TestFSM_parseDSML_noBlocks(t *testing.T) {
	t.Parallel()
	input := `Just some plain text, no DSML blocks.`
	got := parseDSML(input)
	if len(got) != 0 {
		t.Fatalf("expected 0")
	}
}

func TestFSM_parseDSML_deepseekV4_nestedContent(t *testing.T) {
	t.Parallel()
	input := "<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"bash\"\n｜DSML｜parameter name=\"cmd\" string=\"true\">go test -run TestFoo -v ./internal/...</｜DSML｜parameter>\n/｜DSML｜invoke\n</｜DSML｜tool_calls>"
	got := parseDSML(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Tool != "CMD" || got[0].Content != "go test -run TestFoo -v ./internal/..." {
		t.Errorf("bad")
	}
}

func TestFSM_parseDSML_unclosedTag(t *testing.T) {
	t.Parallel()
	input := `<DSML><invoke name="bash"><parameter name="cmd">echo hello`
	got := parseDSML(input)
	if len(got) != 0 {
		t.Fatalf("expected 0")
	}
}

func TestFSM_parseDSML_emptyBlock(t *testing.T) {
	t.Parallel()
	input := `<DSML></DSML>`
	got := parseDSML(input)
	if len(got) != 0 {
		t.Fatalf("expected 0")
	}
}

func TestFSM_parseDSML_multipleBlocks(t *testing.T) {
	t.Parallel()
	input := `<DSML><invoke name="bash"><parameter name="cmd">echo a</parameter></invoke></DSML>` + `
` + `prose` + `
` + `<DSML><invoke name="bash"><parameter name="cmd">echo b</parameter></invoke></DSML>`
	got := parseDSML(input)
	if len(got) != 2 {
		t.Fatalf("expected 2")
	}
	if got[0].Content != "echo a" {
		t.Errorf("bad")
	}
	if got[1].Content != "echo b" {
		t.Errorf("bad")
	}
}

func TestFSM_parseDSML_parameterNameVariants(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, input, tool, content string }{
		{`name=command`, `<DSML><invoke name="bash"><parameter name="cmd">ls</parameter></invoke></DSML>`, `CMD`, `ls`},
		{`name=bash`, `<DSML><invoke name="bash"><parameter name="bash">ls</parameter></invoke></DSML>`, `CMD`, `ls`},
		{`name=file_path`, `<DSML><invoke name="read"><parameter name="fp">foo.go</parameter></invoke></DSML>`, `READ`, `foo.go`},
		{`name=path`, `<DSML><invoke name="read"><parameter name="path">foo.go</parameter></invoke></DSML>`, `READ`, `foo.go`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseDSML(tt.input)
			if len(got) != 1 {
				t.Fatalf("expected 1")
			}
			if got[0].Tool != tt.tool || got[0].Content != tt.content {
				t.Errorf("mismatch")
			}
		})
	}
}

func TestFSM_parseDSML_standardFormatNameVariants(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, input, tool, content string }{
		{`bash`, `<DSML><invoke name="bash"><parameter name="cmd">ls -la</parameter></invoke></DSML>`, `CMD`, `ls -la`},
		{`read`, `<DSML><invoke name="read"><parameter name="fp">README.md</parameter></invoke></DSML>`, `READ`, `README.md`},
		{`write`, `<DSML><invoke name="wf"><parameter name="fp">new.go</parameter></invoke></DSML>`, `WRITE`, `new.go`},
		{`edit`, `<DSML><invoke name="ef"><parameter name="fp">old.go</parameter></invoke></DSML>`, `EDIT`, `old.go`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseDSML(tt.input)
			if len(got) != 1 {
				t.Fatalf("expected 1")
			}
			if got[0].Tool != tt.tool || got[0].Content != tt.content {
				t.Errorf("mismatch")
			}
		})
	}
}

func TestFSM_parseDSML_closingTagVariants(t *testing.T) {
	t.Parallel()
	input := "<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"bash\">\n<｜DSML｜parameter name=\"cmd\" string=\"true\">echo hi</｜DSML｜parameter>\n</｜DSML｜invoke>\n</｜DSML｜tool_calls>"
	got := parseDSML(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Tool != "CMD" || got[0].Content != "echo hi" {
		t.Errorf("bad")
	}
}

func TestFSM_parseFunctionXML_basic(t *testing.T) {
	t.Parallel()
	input := `<invoke name="bash"><parameter name="cmd" string="true">echo fn</parameter></invoke>`
	got := parseFunctionXML(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Tool != "CMD" || got[0].Content != "echo fn" {
		t.Errorf("bad")
	}
}

func TestFSM_parseFunctionXML_multiParam(t *testing.T) {
	t.Parallel()
	input := `<invoke name="wf"><parameter name="fp">f.go</parameter><parameter name="content">pkg main</parameter></invoke>`
	got := parseFunctionXML(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Tool != "WRITE" {
		t.Errorf("bad tool")
	}
}

func TestFSM_parseFunctionXML_noMatch(t *testing.T) {
	t.Parallel()
	input := `just text`
	got := parseFunctionXML(input)
	if len(got) != 0 {
		t.Fatalf("expected 0")
	}
}

func TestFSM_parseFunctionXML_nested(t *testing.T) {
	t.Parallel()
	input := `<invoke name="bash"><parameter name="cmd">echo <inner></inner></parameter></invoke>`
	got := parseFunctionXML(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Content != "echo <inner></inner>" {
		t.Errorf("bad")
	}
}

func TestFSM_parseCmdTag_bash(t *testing.T) {
	t.Parallel()
	input := `<cmd>ls -la</cmd>`
	got := parseCmdTag(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Tool != "CMD" || got[0].Content != "ls -la" {
		t.Errorf("bad")
	}
}

func TestFSM_parseCmdTag_multiline(t *testing.T) {
	t.Parallel()
	input := "<cmd>go test\n./...</cmd>"
	got := parseCmdTag(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Content != "go test\n./..." {
		t.Errorf("bad")
	}
}

func TestFSM_parseCmdTag_multiple(t *testing.T) {
	t.Parallel()
	input := `first <cmd>echo a</cmd> second <cmd>echo b</cmd> third`
	got := parseCmdTag(input)
	if len(got) != 2 {
		t.Fatalf("expected 2")
	}
	if got[0].Content != "echo a" || got[1].Content != "echo b" {
		t.Errorf("bad")
	}
}

func TestFSM_parseCmdTag_unclosed(t *testing.T) {
	t.Parallel()
	input := `<cmd>echo hello`
	got := parseCmdTag(input)
	if len(got) != 0 {
		t.Fatalf("expected 0")
	}
}

func TestFSM_parseCmdTag_empty(t *testing.T) {
	t.Parallel()
	input := `<cmd></cmd>`
	got := parseCmdTag(input)
	if len(got) != 0 {
		t.Fatalf("expected 0")
	}
}

func TestFSM_parseJSON_toolCalls(t *testing.T) {
	t.Parallel()
	input := `{"tool_calls":[{"name":"bash","args":{"cmd":"echo json"}}]}`
	got := parseJSON(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Tool != "CMD" || got[0].Content != "echo json" {
		t.Errorf("bad")
	}
}

func TestFSM_parseJSON_assistantMessage(t *testing.T) {
	t.Parallel()
	input := `{"content":"ok","tool_calls":[{"name":"read","args":{"fp":"f.go"}}]}`
	got := parseJSON(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Tool != "READ" {
		t.Errorf("bad")
	}
}

func TestFSM_parseJSON_noToolCalls(t *testing.T) {
	t.Parallel()
	input := `{"content":"just text"}`
	got := parseJSON(input)
	if len(got) != 0 {
		t.Fatalf("expected 0")
	}
}

func TestFSM_parseJSON_multiple(t *testing.T) {
	t.Parallel()
	input := `{"tool_calls":[{"name":"bash","args":{"cmd":"a"}},{"name":"read","args":{"fp":"b"}}]}`
	got := parseJSON(input)
	if len(got) != 2 {
		t.Fatalf("expected 2")
	}
	if got[0].Content != "a" || got[1].Content != "b" {
		t.Errorf("bad")
	}
}

func TestFSM_parseJSON_invalid(t *testing.T) {
	t.Parallel()
	input := `not json at all`
	got := parseJSON(input)
	if len(got) != 0 {
		t.Fatalf("expected 0")
	}
}

func TestFSM_parseJSON_nestedArgs(t *testing.T) {
	t.Parallel()
	input := `{"tool_calls":[{"name":"wf","args":{"fp":"f.go","content":"pkg main"}}]}`
	got := parseJSON(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Tool != "WRITE" {
		t.Errorf("bad")
	}
}

func TestFSM_parseJSON_codeBlock(t *testing.T) {
	t.Parallel()
	input := "```json\n{\"tool_calls\":[{\"name\":\"bash\",\"args\":{\"cmd\":\"echo hi\"}}]}\n```"
	got := parseJSON(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Content != "echo hi" {
		t.Errorf("bad")
	}
}

func TestFSM_parseJSON_escapeChars(t *testing.T) {
	t.Parallel()
	input := "{\"tool_calls\":[{\"name\":\"bash\",\"args\":{\"cmd\":\"echo \\\"hi\\\"\"}}]}"
	got := parseJSON(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Content != "echo \"hi\"" {
		t.Errorf("bad")
	}
}

func TestFSM_parseJSON_functionCall(t *testing.T) {
	t.Parallel()
	input := `{"function":"read_file","args":"{\"fp\":\"f.go\"}"}`
	got := parseJSON(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Tool != "READ" {
		t.Errorf("bad")
	}
}

func TestFSM_parseMarkdownFence_bash(t *testing.T) {
	t.Parallel()
	input := "```bash\necho hi\n```"
	got := parseMarkdownFence(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Tool != "CMD" || got[0].Content != "echo hi" {
		t.Errorf("bad")
	}
}

func TestFSM_parseMarkdownFence_multiline(t *testing.T) {
	t.Parallel()
	input := "```bash\necho a\necho b\n```"
	got := parseMarkdownFence(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Content != "echo a\necho b" {
		t.Errorf("bad")
	}
}

func TestFSM_parseMarkdownFence_unclosed(t *testing.T) {
	t.Parallel()
	input := "```bash\necho hi"
	got := parseMarkdownFence(input)
	if len(got) != 0 {
		t.Fatalf("expected 0")
	}
}

func TestFSM_parseMarkdownFence_noLang(t *testing.T) {
	t.Parallel()
	input := "```\necho hi\n```"
	got := parseMarkdownFence(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Tool != "CMD" {
		t.Errorf("bad")
	}
}

func TestFSM_parseMarkdownFence_goCode(t *testing.T) {
	t.Parallel()
	input := "```go\nfunc main() {}\n```"
	got := parseMarkdownFence(input)
	if len(got) != 0 {
		t.Fatalf("expected 0")
	}
}

func TestFSM_Integration_mixedFormats(t *testing.T) {
	t.Parallel()
	input := `<DSML><invoke name="bash"><parameter name="cmd">echo dsml</parameter></invoke></DSML>` + "\n" + `<cmd>echo cmd</cmd>` + "\n" + "```bash\necho fence\n```"
	got := parseOrchestrated(input)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
}

func TestFSM_Integration_dsmlOnly(t *testing.T) {
	t.Parallel()
	input := "<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"bash\"\n｜DSML｜parameter name=\"cmd\" string=\"true\">echo only</｜DSML｜parameter>\n/｜DSML｜invoke\n</｜DSML｜tool_calls>"
	got := parseOrchestrated(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Tool != "CMD" {
		t.Errorf("bad")
	}
}

func TestFSM_Integration_proseOnly(t *testing.T) {
	t.Parallel()
	input := `Just some discussion text with no tool calls.`
	got := parseOrchestrated(input)
	if len(got) != 0 {
		t.Fatalf("expected 0")
	}
}

func TestFSM_Integration_realWorld(t *testing.T) {
	t.Parallel()
	input := "Okay, I will run the tests.\n\n<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"bash\"\n｜DSML｜parameter name=\"cmd\" string=\"true\">cd finally && go test ./...</｜DSML｜parameter>\n/｜DSML｜invoke\n</｜DSML｜tool_calls>"
	got := parseOrchestrated(input)
	if len(got) != 1 {
		t.Fatalf("expected 1")
	}
	if got[0].Content != "cd finally && go test ./..." {
		t.Errorf("bad")
	}
}
