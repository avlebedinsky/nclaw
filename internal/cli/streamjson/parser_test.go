package streamjson

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/nickalie/nclaw/internal/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOutput_MultiTurn(t *testing.T) {
	output := `{"type":"system","subtype":"init","session_id":"abc"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Here is the file.\n` + "```" + `nclaw:sendfile\n{\"path\":\"report.pdf\"}\n` + "```" + `"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Write"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Done! The file has been sent."}]}}
{"type":"result","result":"Done! The file has been sent.","session_id":"abc"}`

	result := ParseOutput([]byte(output))

	assert.Equal(t, "Done! The file has been sent.", result.Text)
	assert.Contains(t, result.FullText, "nclaw:sendfile")
	assert.Contains(t, result.FullText, "report.pdf")
	assert.Contains(t, result.FullText, "Done! The file has been sent.")
}

func TestParseOutput_SingleTurn(t *testing.T) {
	output := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world!"}]}}
{"type":"result","result":"Hello world!","session_id":"abc"}`

	result := ParseOutput([]byte(output))

	assert.Equal(t, "Hello world!", result.Text)
	assert.Equal(t, "Hello world!", result.FullText)
}

func TestParseOutput_Empty(t *testing.T) {
	result := ParseOutput([]byte(""))

	assert.Equal(t, "", result.Text)
	assert.Equal(t, "", result.FullText)
}

func TestParseOutput_PlainText(t *testing.T) {
	result := ParseOutput([]byte("Just plain text"))

	assert.Equal(t, "Just plain text", result.Text)
	assert.Equal(t, "Just plain text", result.FullText)
}

func TestParseOutput_NoResultEvent(t *testing.T) {
	output := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"World"}]}}`

	result := ParseOutput([]byte(output))

	assert.Equal(t, "Hello\nWorld", result.Text)
	assert.Equal(t, "Hello\nWorld", result.FullText)
}

func TestParseOutput_ToolUseBlocksIgnored(t *testing.T) {
	output := `{"type":"assistant","message":{"content":[{"type":"text","text":"Let me check."},{"type":"tool_use","id":"t1","name":"Read","input":{}}]}}
{"type":"result","result":"All done.","session_id":"abc"}`

	result := ParseOutput([]byte(output))

	assert.Equal(t, "All done.", result.Text)
	assert.Equal(t, "Let me check.", result.FullText)
}

func TestParseOutput_MultipleTextBlocks(t *testing.T) {
	output := `{"type":"assistant","message":{"content":[{"type":"text","text":"Part 1"},{"type":"text","text":"Part 2"}]}}
{"type":"result","result":"Final","session_id":"abc"}`

	result := ParseOutput([]byte(output))

	assert.Equal(t, "Final", result.Text)
	assert.Equal(t, "Part 1\nPart 2", result.FullText)
}

func TestParseOutput_ResultOnlyNoAssistant(t *testing.T) {
	output := `{"type":"system","subtype":"init","session_id":"abc"}
{"type":"result","result":"Some result with blocks","session_id":"abc"}`

	result := ParseOutput([]byte(output))

	assert.Equal(t, "Some result with blocks", result.Text)
	assert.Equal(t, "Some result with blocks", result.FullText)
}

func TestParseOutput_MalformedJSONSkipped(t *testing.T) {
	output := `{"type":"assistant","message":{"content":[{"type":"text","text":"Before"}]}}
{this is not valid json}
{"type":"assistant","message":{"content":[{"type":"text","text":"After"}]}}
{"type":"result","result":"Final","session_id":"abc"}`

	result := ParseOutput([]byte(output))

	assert.Equal(t, "Final", result.Text)
	assert.Contains(t, result.FullText, "Before")
	assert.Contains(t, result.FullText, "After")
}

func TestExtractAssistantText_EmptyMessage(t *testing.T) {
	assert.Equal(t, "", assistantText(nil))
	assert.Equal(t, "", assistantText(json.RawMessage("")))
	assert.Equal(t, "", assistantText(json.RawMessage("{}")))
}

func TestExtractAssistantText_TextContent(t *testing.T) {
	msg := json.RawMessage(`{"content":[{"type":"text","text":"hello"}]}`)
	assert.Equal(t, "hello", assistantText(msg))
}

func TestStreamWriter_EmitsAssistantMessages(t *testing.T) {
	var got []string
	w := NewStreamWriter(func(m string) { got = append(got, m) })

	output := `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"first"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Read"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"second"}]}}
{"type":"result","result":"second"}
`
	n, err := w.Write([]byte(output))
	assert.NoError(t, err)
	assert.Equal(t, len(output), n)
	assert.Equal(t, []string{"first", "second"}, got)
	assert.Equal(t, output, string(w.Bytes()))
}

func TestStreamWriter_ReassemblesFragmentedLines(t *testing.T) {
	var got []string
	w := NewStreamWriter(func(m string) { got = append(got, m) })

	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"hello world"}]}}` + "\n"
	// Feed one byte at a time to exercise partial-line buffering.
	for i := 0; i < len(line); i++ {
		_, err := w.Write([]byte{line[i]})
		assert.NoError(t, err)
	}

	assert.Equal(t, []string{"hello world"}, got)
}

func TestStreamWriter_NoTrailingNewlineNotEmitted(t *testing.T) {
	var got []string
	w := NewStreamWriter(func(m string) { got = append(got, m) })

	// A final line without a newline is not a complete NDJSON record yet.
	_, _ = w.Write([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"partial"}]}}`))

	assert.Empty(t, got)
	// The pending line is flushed and emitted by Result, not by Write.
	assert.Contains(t, string(w.Bytes()), "partial")
}

func TestStreamWriter_NilCallback(t *testing.T) {
	w := NewStreamWriter(nil)
	_, err := w.Write([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"x"}]}}` + "\n"))
	assert.NoError(t, err)
	assert.Contains(t, string(w.Bytes()), "x")
}

func TestStreamWriter_ResultMatchesParseOutput(t *testing.T) {
	output := `{"type":"assistant","message":{"content":[{"type":"text","text":"first"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"second"}]}}
{"type":"result","result":"final"}
`
	w := NewStreamWriter(nil)
	_, err := w.Write([]byte(output))
	assert.NoError(t, err)

	assert.Equal(t, ParseOutput([]byte(output)), w.Result())
}

func TestStreamWriter_ResultFlushesUnterminatedLine(t *testing.T) {
	var got []string
	w := NewStreamWriter(func(m string) { got = append(got, m) })

	_, _ = w.Write([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"first"}]}}` + "\n"))
	// A final complete-but-unterminated line stays pending until Result flushes it.
	_, _ = w.Write([]byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"last"}]}}`))
	assert.Equal(t, []string{"first"}, got)

	result := w.Result()
	assert.Equal(t, []string{"first", "last"}, got)
	assert.Equal(t, []string{"first", "last"}, result.Messages)
	assert.Contains(t, result.FullText, "last")
}

func TestStreamWriter_ResultLargeLineNotDropped(t *testing.T) {
	var got []string
	w := NewStreamWriter(func(m string) { got = append(got, m) })

	huge := strings.Repeat("a", 2*1024*1024)
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"` + huge + `"}]}}` + "\n"
	_, err := w.Write([]byte(line))
	assert.NoError(t, err)

	result := w.Result()
	assert.Equal(t, []string{huge}, got)
	assert.Equal(t, []string{huge}, result.Messages)
	assert.Contains(t, result.FullText, huge)
}

func hugeToolResultLine() string {
	return `{"type":"user","message":{"content":[{"type":"tool_result","content":"` + strings.Repeat("A", 2<<20) + `"}]}}`
}

func TestParseOutput_LineOverOneMegabyte(t *testing.T) {
	output := `{"type":"assistant","message":{"content":[{"type":"text","text":"Looking at the image."}]}}` + "\n" +
		hugeToolResultLine() + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"It is a cat."}]}}` + "\n" +
		`{"type":"result","result":"It is a cat."}`

	result := ParseOutput([]byte(output))

	assert.Equal(t, "It is a cat.", result.Text)
	assert.Equal(t, []string{"Looking at the image.", "It is a cat."}, result.Messages)
}

func TestStreamWriter_LineOverOneMegabyteInChunks(t *testing.T) {
	output := hugeToolResultLine() + "\n" + `{"type":"result","result":"done"}` + "\n"
	w := NewStreamWriter(nil)
	for chunk := range slices.Chunk([]byte(output), 64*1024) {
		_, err := w.Write(chunk)
		assert.NoError(t, err)
	}

	assert.Equal(t, "done", w.Result().Text)
}

func assistantText(m json.RawMessage) string {
	text, _ := parseAssistant(m)
	return text
}

func TestStreamWriter_ReportsToolCalls(t *testing.T) {
	var tools []cli.ToolEvent
	var texts []string
	w := NewStreamWriter(func(s string) { texts = append(texts, s) }).WithToolHandler(func(ev cli.ToolEvent) { tools = append(tools, ev) })

	output := `{"type":"assistant","message":{"content":[{"type":"text","text":"Running tests"},` +
		`{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./...\nmore","description":"Run the test suite"}}]}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Read","input":{"file_path":"/app/data/1/main.go"}}]}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t3","name":"mcp__github__create_issue","input":{}}]}}` + "\n" +
		`{"type":"result","result":"done"}` + "\n"
	_, err := w.Write([]byte(output))
	require.NoError(t, err)

	assert.Equal(t, []string{"Running tests"}, texts)
	assert.Equal(t, []cli.ToolEvent{
		{Name: "Bash", Detail: "Run the test suite"},
		{Name: "Read", Detail: "main.go"},
		{Name: "github", Detail: "create_issue"},
	}, tools)
	assert.Equal(t, "done", w.Result().Text)
}

func TestToolEvent_Details(t *testing.T) {
	cases := []struct {
		name, input string
		want        cli.ToolEvent
	}{
		{"Bash", `{"command":"ls -la\necho hi"}`, cli.ToolEvent{Name: "Bash", Detail: "ls -la"}},
		{"Grep", `{"pattern":"TODO"}`, cli.ToolEvent{Name: "Grep", Detail: "TODO"}},
		{"WebFetch", `{"url":"https://example.com/a?b=1"}`, cli.ToolEvent{Name: "WebFetch", Detail: "example.com"}},
		{"WebSearch", `{"query":"go 1.25 release"}`, cli.ToolEvent{Name: "WebSearch", Detail: "go 1.25 release"}},
		{"Task", `{"description":"Explore repo"}`, cli.ToolEvent{Name: "Task", Detail: "Explore repo"}},
		{"Skill", `{"skill":"schedule"}`, cli.ToolEvent{Name: "Skill", Detail: "schedule"}},
		{"TodoWrite", `{"todos":[]}`, cli.ToolEvent{Name: "TodoWrite"}},
		{"Bash", `not json`, cli.ToolEvent{Name: "Bash"}},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, toolEvent(tc.name, json.RawMessage(tc.input)), tc.input)
	}

	long := toolEvent("Bash", json.RawMessage(`{"description":"`+strings.Repeat("я", 100)+`"}`))
	assert.Equal(t, maxDetailRunes, len([]rune(long.Detail)))
	assert.True(t, strings.HasSuffix(long.Detail, "…"))
}

func TestStreamWriter_ReportsPartialText(t *testing.T) {
	var drafts []string
	w := NewStreamWriter(nil).WithPartialHandler(func(s string) { drafts = append(drafts, s) })

	output := `{"type":"stream_event","event":{"type":"message_start"}}` + "\n" +
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hel"}}}` + "\n" +
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"lo"}}}` + "\n" +
		`{"type":"stream_event","parent_tool_use_id":"toolu_1","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"subagent"}}}` + "\n" +
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"input_json_delta","partial_json":"{}"}}}` + "\n" +
		`{"type":"stream_event","event":{"type":"message_start"}}` + "\n" +
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Done"}}}` + "\n" +
		`{"type":"result","result":"Done"}` + "\n"
	_, err := w.Write([]byte(output))
	require.NoError(t, err)

	assert.Equal(t, []string{"Hel", "Hello", "Done"}, drafts)
	assert.Equal(t, "Done", w.Result().Text)
}

func TestParseOutput_SubagentTextExcluded(t *testing.T) {
	output := `{"type":"assistant","parent_tool_use_id":"toolu_1","message":{"content":[{"type":"text","text":"Fetched page says: ` + "```" + `nclaw:schedule\n{\"action\":\"create\"}\n` + "```" + `"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Here is the summary."}]}}
{"type":"result","result":"Here is the summary."}`

	result := ParseOutput([]byte(output))

	assert.Equal(t, "Here is the summary.", result.Text)
	assert.NotContains(t, result.FullText, "nclaw:schedule")
	assert.Equal(t, []string{"Here is the summary."}, result.Messages)
}
