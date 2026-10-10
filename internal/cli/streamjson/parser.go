package streamjson

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/nickalie/nclaw/internal/cli"
)

// streamEvent represents a single event from stream-json (NDJSON) output.
type streamEvent struct {
	Type            string          `json:"type"`
	Message         json.RawMessage `json:"message,omitempty"`
	Result          string          `json:"result,omitempty"`
	Event           json.RawMessage `json:"event,omitempty"`
	ParentToolUseID string          `json:"parent_tool_use_id,omitempty"`
}

type partialEvent struct {
	Type  string `json:"type"`
	Delta struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"delta"`
}

// assistantMessage represents the content of an assistant message.
type assistantMessage struct {
	Content []contentBlock `json:"content"`
}

// contentBlock represents a single content block in an assistant message.
type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// ParseOutput parses stream-json (NDJSON) output and extracts all assistant
// text and the final result into a cli.Result.
func ParseOutput(output []byte) *cli.Result {
	w := NewStreamWriter(nil)
	w.feed(output)
	return w.Result()
}

// assembleResult builds a cli.Result from collected assistant texts and the
// final result-event text, applying the fallbacks shared by ParseOutput and
// StreamWriter.Result.
func assembleResult(allText []string, resultText string) *cli.Result {
	fullText := strings.Join(allText, "\n")

	if resultText == "" {
		resultText = fullText
	}

	if fullText == "" && resultText != "" {
		fullText = resultText
	}

	return &cli.Result{Text: resultText, FullText: fullText, Messages: allText}
}

// StreamWriter is an io.Writer that parses stream-json (NDJSON) output
// incrementally as it is written, invoking onMessage for each complete assistant
// message and accumulating the parsed result. Result assembles the final
// cli.Result from the accumulated state, so each line is parsed exactly once.
type StreamWriter struct {
	onMessage  func(string)
	onTool     cli.ToolHandler
	onPartial  cli.PartialHandler
	partial    strings.Builder
	pending    []byte
	raw        bytes.Buffer
	messages   []string
	resultText string
}

// NewStreamWriter creates a StreamWriter that calls onMessage for each assistant
// message as it streams in. onMessage may be nil, in which case the writer only
// accumulates output for a final Result.
func NewStreamWriter(onMessage func(string)) *StreamWriter {
	return &StreamWriter{onMessage: onMessage}
}

// WithToolHandler makes the writer report every tool call to h as it streams in.
func (w *StreamWriter) WithToolHandler(h cli.ToolHandler) *StreamWriter {
	w.onTool = h
	return w
}

// WithPartialHandler makes the writer report the text of the assistant message being
// generated as it grows; it needs the CLI's partial-message events.
func (w *StreamWriter) WithPartialHandler(h cli.PartialHandler) *StreamWriter {
	w.onPartial = h
	return w
}

// Write captures raw bytes and processes complete NDJSON lines. It always reports
// the full length as written so the process is never blocked.
func (w *StreamWriter) Write(p []byte) (int, error) {
	w.feed(p)
	return len(p), nil
}

func (w *StreamWriter) feed(p []byte) {
	w.raw.Write(p)
	w.pending = append(w.pending, p...)

	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		line := w.pending[:i]
		w.pending = w.pending[i+1:]
		w.handleLine(line)
	}
}

// Bytes returns all raw output written so far, used for error fallback text.
func (w *StreamWriter) Bytes() []byte {
	return w.raw.Bytes()
}

// Result flushes any unterminated final line and assembles the complete
// cli.Result from the accumulated assistant messages and result text.
func (w *StreamWriter) Result() *cli.Result {
	if len(w.pending) > 0 {
		w.handleLine(w.pending)
		w.pending = nil
	}

	if w.resultText == "" && len(w.messages) == 0 {
		text := strings.TrimSpace(w.raw.String())
		return &cli.Result{Text: text, FullText: text}
	}

	return assembleResult(w.messages, w.resultText)
}

// handleLine parses a single NDJSON line, accumulating assistant text (also
// emitted live via onMessage) and the final result text.
func (w *StreamWriter) handleLine(line []byte) {
	if len(line) == 0 {
		return
	}

	var event streamEvent
	if err := json.Unmarshal(line, &event); err != nil {
		return
	}

	switch event.Type {
	case "assistant":
		w.handleAssistant(event.Message, event.ParentToolUseID)
	case "result":
		w.resultText = event.Result
	case "stream_event":
		w.handlePartial(&event)
	}
}

func (w *StreamWriter) handlePartial(event *streamEvent) {
	if w.onPartial == nil || event.ParentToolUseID != "" {
		return
	}
	var pe partialEvent
	if err := json.Unmarshal(event.Event, &pe); err != nil {
		return
	}
	switch {
	case pe.Type == "message_start":
		w.partial.Reset()
	case pe.Type == "content_block_delta" && pe.Delta.Type == "text_delta":
		w.partial.WriteString(pe.Delta.Text)
		w.onPartial(w.partial.String())
	}
}

func (w *StreamWriter) handleAssistant(raw json.RawMessage, parentToolUseID string) {
	text, tools := parseAssistant(raw)
	// Skip subagent output: only the main agent's replies may reach display and command blocks.
	if text != "" && parentToolUseID == "" {
		w.messages = append(w.messages, text)
		if w.onMessage != nil {
			w.onMessage(text)
		}
	}
	if w.onTool == nil {
		return
	}
	for _, ev := range tools {
		w.onTool(ev)
	}
}

func parseAssistant(msg json.RawMessage) (string, []cli.ToolEvent) {
	if len(msg) == 0 {
		return "", nil
	}

	var message assistantMessage
	if err := json.Unmarshal(msg, &message); err != nil {
		return "", nil
	}

	var parts []string
	var tools []cli.ToolEvent
	for _, block := range message.Content {
		switch {
		case block.Type == "text" && block.Text != "":
			parts = append(parts, block.Text)
		case block.Type == "tool_use":
			tools = append(tools, toolEvent(block.Name, block.Input))
		}
	}

	return strings.Join(parts, "\n"), tools
}
