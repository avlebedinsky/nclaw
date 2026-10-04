package telegram

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSplitMessage_ShortMessage(t *testing.T) {
	result := SplitMessage("hello", 100)
	assert.Equal(t, []string{"hello"}, result)
}

func TestSplitMessage_ExactMaxLen(t *testing.T) {
	msg := strings.Repeat("a", 50)
	result := SplitMessage(msg, 50)
	assert.Equal(t, []string{msg}, result)
}

func TestSplitMessage_SplitAtNewline(t *testing.T) {
	msg := "line1\nline2\nline3"
	result := SplitMessage(msg, 10)
	assert.Equal(t, []string{"line1", "line2", "line3"}, result)
}

func TestSplitMessage_NoNewlineForceSplit(t *testing.T) {
	msg := strings.Repeat("a", 20)
	result := SplitMessage(msg, 10)
	assert.Equal(t, []string{strings.Repeat("a", 10), strings.Repeat("a", 10)}, result)
}

func TestSplitMessage_MultipleChunks(t *testing.T) {
	msg := "aaaa\nbbbb\ncccc\ndddd"
	result := SplitMessage(msg, 9)
	assert.Equal(t, []string{"aaaa\nbbbb", "cccc\ndddd"}, result)
}

func TestSplitMessage_EmptyString(t *testing.T) {
	result := SplitMessage("", 100)
	assert.Equal(t, []string{""}, result)
}

func TestChatDir_NoThread(t *testing.T) {
	dir := ChatDir("/data", 12345, 0)
	assert.Equal(t, "/data/12345", dir)
}

func TestChatDir_WithThread(t *testing.T) {
	dir := ChatDir("/data", 12345, 99)
	assert.Equal(t, "/data/12345/99", dir)
}

func TestChatDir_NegativeChatID(t *testing.T) {
	dir := ChatDir("/data", -100123, 0)
	assert.Equal(t, "/data/-100123", dir)
}

func TestPromptExplainsButtons(t *testing.T) {
	assert.Contains(t, Prompt, "Format all responses using Telegram HTML")
	assert.Contains(t, Prompt, "```nclaw:buttons\n[\"Yes, delete\", \"No\"]\n```")
}
