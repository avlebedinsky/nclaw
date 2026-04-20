package copilot

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log"
	"strings"

	"github.com/nickalie/nclaw/internal/cli"
)

// copilotEvent represents a single JSONL event from `copilot --output-format=json`.
type copilotEvent struct {
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	ExitCode  int             `json:"exitCode,omitempty"`
	Ephemeral bool            `json:"ephemeral,omitempty"`
}

// assistantMessageData holds the data payload of an assistant.message event.
type assistantMessageData struct {
	Content string `json:"content"`
}

// parseResult holds the parsed output and extracted session ID.
type parseResult struct {
	result    *cli.Result
	sessionID string
}

// parseJSONOutput parses Copilot CLI's JSONL output (--output-format=json) and returns
// a cli.Result with Text=last message, FullText=all messages, plus the session ID.
func parseJSONOutput(output []byte) parseResult {
	messages, sessionID := collectJSONEvents(output)

	if len(messages) == 0 {
		text := strings.TrimSpace(string(output))
		return parseResult{result: &cli.Result{Text: text, FullText: text}}
	}

	fullText := strings.Join(messages, "\n")
	lastMessage := messages[len(messages)-1]

	return parseResult{
		result:    &cli.Result{Text: lastMessage, FullText: fullText},
		sessionID: sessionID,
	}
}

// collectJSONEvents scans JSONL lines and collects non-empty assistant message contents
// and the session ID from the result event.
func collectJSONEvents(output []byte) (messages []string, sessionID string) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var event copilotEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}

		switch event.Type {
		case "assistant.message":
			if content := extractAssistantContent(event.Data); content != "" {
				messages = append(messages, content)
			}
		case "result":
			sessionID = event.SessionID
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("copilot: json scan error (output may be truncated): %v", err)
	}

	return messages, sessionID
}

// extractAssistantContent parses the data payload of an assistant.message event
// and returns the text content, or empty string if there is none.
func extractAssistantContent(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}

	var msg assistantMessageData
	if err := json.Unmarshal(data, &msg); err != nil {
		return ""
	}

	return strings.TrimSpace(msg.Content)
}
