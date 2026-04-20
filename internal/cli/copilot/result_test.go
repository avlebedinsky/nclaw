package copilot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseJSONOutput_SimpleMessage(t *testing.T) {
	input := `{"type":"assistant.message","data":{"messageId":"abc","content":"Hello!","toolRequests":[]},"id":"1"}
{"type":"result","sessionId":"sess-001","exitCode":0}`

	pr := parseJSONOutput([]byte(input))
	assert.Equal(t, "Hello!", pr.result.Text)
	assert.Equal(t, "Hello!", pr.result.FullText)
	assert.Equal(t, "sess-001", pr.sessionID)
}

func TestParseJSONOutput_MultipleMessages(t *testing.T) {
	input := `{"type":"assistant.message","data":{"content":"First response","toolRequests":[]},"id":"1"}
{"type":"assistant.message","data":{"content":"Second response","toolRequests":[]},"id":"2"}
{"type":"result","sessionId":"sess-002","exitCode":0}`

	pr := parseJSONOutput([]byte(input))
	assert.Equal(t, "Second response", pr.result.Text)
	assert.Equal(t, "First response\nSecond response", pr.result.FullText)
	assert.Equal(t, "sess-002", pr.sessionID)
}

func TestParseJSONOutput_SkipsEmptyContent(t *testing.T) {
	// Tool-call messages often have empty content.
	input := `{"type":"assistant.message","data":{"content":"","toolRequests":[{"name":"shell"}]},"id":"1"}
{"type":"assistant.message","data":{"content":"Done!","toolRequests":[]},"id":"2"}
{"type":"result","sessionId":"sess-003","exitCode":0}`

	pr := parseJSONOutput([]byte(input))
	assert.Equal(t, "Done!", pr.result.Text)
	assert.Equal(t, "Done!", pr.result.FullText)
}

func TestParseJSONOutput_EphemeralDeltasIgnored(t *testing.T) {
	input := `{"type":"assistant.message_delta","data":{"deltaContent":"He"},"ephemeral":true,"id":"1"}
{"type":"assistant.message_delta","data":{"deltaContent":"llo"},"ephemeral":true,"id":"2"}
{"type":"assistant.message","data":{"content":"Hello","toolRequests":[]},"id":"3"}
{"type":"result","sessionId":"sess-004","exitCode":0}`

	pr := parseJSONOutput([]byte(input))
	assert.Equal(t, "Hello", pr.result.Text)
}

func TestParseJSONOutput_FallbackOnNoMessages(t *testing.T) {
	input := `{"type":"result","exitCode":1}`

	pr := parseJSONOutput([]byte(input))
	assert.Contains(t, pr.result.Text, `"type":"result"`)
	assert.Equal(t, "", pr.sessionID)
}

func TestParseJSONOutput_NoSessionID(t *testing.T) {
	input := `{"type":"assistant.message","data":{"content":"Hi","toolRequests":[]},"id":"1"}
{"type":"result","exitCode":0}`

	pr := parseJSONOutput([]byte(input))
	assert.Equal(t, "Hi", pr.result.Text)
	assert.Equal(t, "", pr.sessionID)
}

func TestParseJSONOutput_MalformedLinesSkipped(t *testing.T) {
	input := `not-json
{"type":"assistant.message","data":{"content":"OK","toolRequests":[]},"id":"1"}
{"type":"result","sessionId":"sess-005","exitCode":0}`

	pr := parseJSONOutput([]byte(input))
	assert.Equal(t, "OK", pr.result.Text)
	assert.Equal(t, "sess-005", pr.sessionID)
}

func TestParseJSONOutput_ContentTrimmed(t *testing.T) {
	input := `{"type":"assistant.message","data":{"content":"  trimmed  ","toolRequests":[]},"id":"1"}
{"type":"result","sessionId":"sess-006","exitCode":0}`

	pr := parseJSONOutput([]byte(input))
	assert.Equal(t, "trimmed", pr.result.Text)
}

func TestExtractAssistantContent_EmptyData(t *testing.T) {
	assert.Equal(t, "", extractAssistantContent(nil))
}

func TestExtractAssistantContent_ValidContent(t *testing.T) {
	data := []byte(`{"content":"Hello","toolRequests":[]}`)
	assert.Equal(t, "Hello", extractAssistantContent(data))
}
