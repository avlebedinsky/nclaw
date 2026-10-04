package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nickalie/nclaw/internal/buttons"
	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/sendfile"
)

// mockExecutor implements BlockExecutor for testing.
type mockExecutor struct {
	called   bool
	lastText string
	msg      string // status message to return
}

func (m *mockExecutor) ExecuteBlocks(text string, _ int64, _ int) string {
	m.called = true
	m.lastText = text
	return m.msg
}

// mockSend records all sent messages.
type mockSend struct {
	calls []sendCall
	err   error // if set, returned for all calls
}

type sendCall struct {
	chatID    int64
	threadID  int
	text      string
	parseMode string
	replyTo   int
	buttons   buttons.Keyboard
}

func (m *mockSend) fn() SendFunc {
	return func(_ context.Context, dest Dest, text, parseMode string) error {
		m.calls = append(m.calls, sendCall{dest.ChatID, dest.ThreadID, text, parseMode, dest.ReplyTo, dest.Buttons})
		return m.err
	}
}

func TestProcess_SuccessPath(t *testing.T) {
	exec := &mockExecutor{}
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true, exec)

	result := &cli.Result{
		Text:     "Hello world",
		FullText: "Hello world",
	}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	assert.True(t, exec.called)
	assert.Equal(t, "Hello world", exec.lastText)
	require.Len(t, ms.calls, 1)
	assert.Equal(t, int64(100), ms.calls[0].chatID)
	assert.Equal(t, "Hello world", ms.calls[0].text)
	assert.Equal(t, "HTML", ms.calls[0].parseMode)
}

func TestProcess_ErrorPath_SkipsExecution(t *testing.T) {
	exec := &mockExecutor{}
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true, exec)

	result := &cli.Result{
		Text:     "error: something went wrong",
		FullText: "error: something went wrong",
	}
	p.Process(context.Background(), result, errors.New("claude failed"), Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	assert.False(t, exec.called, "executors should not run on error")
	require.Len(t, ms.calls, 1)
	assert.Equal(t, "error: something went wrong", ms.calls[0].text)
}

func TestProcess_NilWebhookExecutor_Filtered(t *testing.T) {
	exec := &mockExecutor{msg: "scheduled ok"}
	ms := &mockSend{}
	// Pass a nil executor alongside a real one — nil should be filtered.
	p := New(ms.fn(), sendfile.Senders{}, true, exec, nil)

	result := &cli.Result{Text: "reply", FullText: "reply"}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	assert.True(t, exec.called)
	assert.Len(t, p.executors, 1, "nil executors should be filtered out")
}

func TestProcess_StatusAppending(t *testing.T) {
	exec1 := &mockExecutor{msg: "[Schedule error: oops]"}
	exec2 := &mockExecutor{msg: "[Webhook created: https://example.com/webhooks/abc]"}
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true, exec1, exec2)

	result := &cli.Result{Text: "Done", FullText: "Done"}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	require.Len(t, ms.calls, 1)
	assert.Contains(t, ms.calls[0].text, "Done")
	assert.Contains(t, ms.calls[0].text, "[Schedule error: oops]")
	assert.Contains(t, ms.calls[0].text, "[Webhook created: https://example.com/webhooks/abc]")
}

func TestProcess_StreamMessages_SendsEachMessage(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)
	p.SetStreamMessages(true)

	result := &cli.Result{
		Text:     "final",
		FullText: "first\nsecond\nfinal",
		Messages: []string{"first", "second", "final"},
	}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	require.Len(t, ms.calls, 3)
	assert.Equal(t, "first", ms.calls[0].text)
	assert.Equal(t, "second", ms.calls[1].text)
	assert.Equal(t, "final", ms.calls[2].text)
}

func TestProcess_StreamMessages_Disabled_SendsOnlyFinal(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)

	result := &cli.Result{
		Text:     "final",
		FullText: "first\nsecond\nfinal",
		Messages: []string{"first", "second", "final"},
	}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	require.Len(t, ms.calls, 1)
	assert.Equal(t, "final", ms.calls[0].text)
}

func TestProcess_StreamMessages_StatusOnLastMessage(t *testing.T) {
	exec := &mockExecutor{msg: "[status]"}
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true, exec)
	p.SetStreamMessages(true)

	result := &cli.Result{
		Text:     "final",
		FullText: "first\nfinal",
		Messages: []string{"first", "final"},
	}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	require.Len(t, ms.calls, 2)
	assert.Equal(t, "first", ms.calls[0].text)
	assert.Contains(t, ms.calls[1].text, "final")
	assert.Contains(t, ms.calls[1].text, "[status]")
}

func TestProcess_StreamMessages_StripsBlocksPerMessage(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)
	p.SetStreamMessages(true)

	result := &cli.Result{
		Text: "done",
		Messages: []string{
			"working\n```nclaw:sendfile\n{\"path\":\"x\"}\n```",
			"done",
		},
	}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	require.Len(t, ms.calls, 2)
	assert.Equal(t, "working", ms.calls[0].text)
	assert.Equal(t, "done", ms.calls[1].text)
}

func TestProcess_StreamMessages_EmptyMessages_FallsBackToText(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)
	p.SetStreamMessages(true)

	result := &cli.Result{Text: "only text", FullText: "only text"}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	require.Len(t, ms.calls, 1)
	assert.Equal(t, "only text", ms.calls[0].text)
}

func TestProcess_EmptyText_NoSend(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)

	result := &cli.Result{Text: "", FullText: ""}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	assert.Empty(t, ms.calls, "should not send empty text")
}

func TestProcess_StripsAllBlockTypes(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)

	text := "Hello\n" +
		"```nclaw:sendfile\n{\"path\":\"test.txt\"}\n```\n" +
		"```nclaw:schedule\n{\"action\":\"create\"}\n```\n" +
		"```nclaw:webhook\n{\"action\":\"list\"}\n```\n" +
		"Goodbye"

	result := &cli.Result{Text: text, FullText: text}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	require.Len(t, ms.calls, 1)
	assert.NotContains(t, ms.calls[0].text, "nclaw:sendfile")
	assert.NotContains(t, ms.calls[0].text, "nclaw:schedule")
	assert.NotContains(t, ms.calls[0].text, "nclaw:webhook")
	assert.Contains(t, ms.calls[0].text, "Hello")
	assert.Contains(t, ms.calls[0].text, "Goodbye")
}

func TestProcess_HTMLFallbackToPlainText(t *testing.T) {
	callCount := 0
	sendFn := func(_ context.Context, _ Dest, _, parseMode string) error {
		callCount++
		if parseMode == "HTML" {
			return fmt.Errorf("HTML parse error")
		}
		return nil
	}
	p := New(sendFn, sendfile.Senders{}, true)

	result := &cli.Result{Text: "Hello", FullText: "Hello"}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	assert.Equal(t, 2, callCount, "should try HTML then plain text")
}

func TestProcess_PlainFallbackStripsTags(t *testing.T) {
	var plain []string
	sendFn := func(_ context.Context, _ Dest, text, parseMode string) error {
		if parseMode == "HTML" {
			return fmt.Errorf("HTML parse error")
		}
		plain = append(plain, text)
		return nil
	}
	p := New(sendFn, sendfile.Senders{}, true)

	result := &cli.Result{Text: "<b>Итог</b>: a &lt; b <p>", FullText: "<b>Итог</b>: a &lt; b <p>"}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	assert.Equal(t, []string{"Итог: a < b <p>"}, plain)
}

func TestProcess_MultipleExecutors(t *testing.T) {
	exec1 := &mockExecutor{}
	exec2 := &mockExecutor{}
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true, exec1, exec2)

	result := &cli.Result{Text: "reply", FullText: "full reply"}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 5}, "/tmp", false)

	assert.True(t, exec1.called)
	assert.True(t, exec2.called)
	assert.Equal(t, "full reply", exec1.lastText)
	assert.Equal(t, "full reply", exec2.lastText)
}

func TestNew_NilExecutorsFiltered(t *testing.T) {
	p := New(nil, sendfile.Senders{}, true, nil, nil)
	assert.Empty(t, p.executors)
}

// streamClient implements cli.Client and cli.StreamingClient for AttachStream tests.
type streamClient struct {
	handler cli.MessageHandler
}

func (c *streamClient) Dir(string) cli.Client                { return c }
func (c *streamClient) Context(context.Context) cli.Client   { return c }
func (c *streamClient) SkipPermissions() cli.Client          { return c }
func (c *streamClient) AppendSystemPrompt(string) cli.Client { return c }
func (c *streamClient) Ask(string) (*cli.Result, error)      { return &cli.Result{}, nil }
func (c *streamClient) Continue(string) (*cli.Result, error) { return &cli.Result{}, nil }
func (c *streamClient) OnMessage(h cli.MessageHandler) cli.Client {
	c.handler = h
	return c
}

// plainClient implements only cli.Client (no streaming support).
type plainClient struct{}

func (c *plainClient) Dir(string) cli.Client                { return c }
func (c *plainClient) Context(context.Context) cli.Client   { return c }
func (c *plainClient) SkipPermissions() cli.Client          { return c }
func (c *plainClient) AppendSystemPrompt(string) cli.Client { return c }
func (c *plainClient) Ask(string) (*cli.Result, error)      { return &cli.Result{}, nil }
func (c *plainClient) Continue(string) (*cli.Result, error) { return &cli.Result{}, nil }

func TestAttachStream_Disabled_ReturnsNil(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)

	st := p.AttachStream(context.Background(), &streamClient{}, Dest{ChatID: 100, ThreadID: 0})
	assert.Nil(t, st)
	assert.False(t, st.Streamed())
}

func TestAttachStream_NonStreamingClient_ReturnsNil(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)
	p.SetStreamMessages(true)

	st := p.AttachStream(context.Background(), &plainClient{}, Dest{ChatID: 100, ThreadID: 0})
	assert.Nil(t, st)
}

func TestAttachStream_NilPipeline_ReturnsNil(t *testing.T) {
	var p *Pipeline
	st := p.AttachStream(context.Background(), &streamClient{}, Dest{ChatID: 100, ThreadID: 0})
	assert.Nil(t, st)
}

func TestAttachStream_SendsLiveAndStripsBlocks(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)
	p.SetStreamMessages(true)

	client := &streamClient{}
	st := p.AttachStream(context.Background(), client, Dest{ChatID: 100, ThreadID: 0})
	require.NotNil(t, st)
	require.NotNil(t, client.handler)

	client.handler("hello")
	client.handler("```nclaw:sendfile\n{\"path\":\"x\"}\n```") // block-only → nothing sent
	client.handler("world")

	require.Len(t, ms.calls, 2)
	assert.Equal(t, "hello", ms.calls[0].text)
	assert.Equal(t, "world", ms.calls[1].text)
	assert.True(t, st.Streamed())
}

func TestProcess_Streamed_SkipsDisplayButRunsBlocks(t *testing.T) {
	exec := &mockExecutor{msg: "[status]"}
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true, exec)
	p.SetStreamMessages(true)

	result := &cli.Result{
		Text:     "final",
		FullText: "first\nfinal",
		Messages: []string{"first", "final"},
	}
	// streamed=true: messages already delivered live, only status should be sent.
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", true)

	assert.True(t, exec.called, "block execution still runs on FullText")
	assert.Equal(t, "first\nfinal", exec.lastText)
	require.Len(t, ms.calls, 1)
	assert.Equal(t, "[status]", ms.calls[0].text)
}

func TestProcess_Streamed_NoStatus_SendsNothing(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)
	p.SetStreamMessages(true)

	result := &cli.Result{Text: "final", FullText: "final", Messages: []string{"final"}}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", true)

	assert.Empty(t, ms.calls, "streamed messages must not be re-sent")
}

func TestAppendStatus_Empty(t *testing.T) {
	assert.Equal(t, "hello", appendStatus("hello", nil))
	assert.Equal(t, "hello", appendStatus("hello", []string{}))
}

func TestAppendStatus_WithMessages(t *testing.T) {
	result := appendStatus("text", []string{"msg1", "msg2"})
	assert.Equal(t, "text\n\nmsg1\n\nmsg2", result)
}

func TestAppendStatus_EmptyBase(t *testing.T) {
	result := appendStatus("", []string{"msg"})
	assert.Equal(t, "msg", result)
}

func TestProcess_WebhooksNotConfigured_WarningAppended(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, false) // webhooksConfigured=false

	text := "Here you go.\n```nclaw:webhook\n{\"action\":\"create\",\"description\":\"test\"}\n```\nDone!"
	result := &cli.Result{Text: text, FullText: text}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	require.Len(t, ms.calls, 1)
	assert.Contains(t, ms.calls[0].text, "Here you go.")
	assert.Contains(t, ms.calls[0].text, "Done!")
	assert.NotContains(t, ms.calls[0].text, "nclaw:webhook")
	assert.Contains(t, ms.calls[0].text, "[Webhooks are not configured on this instance]")
}

func TestProcess_WebhooksConfigured_NoWarning(t *testing.T) {
	exec := &mockExecutor{}
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true, exec) // webhooksConfigured=true

	text := "Done.\n```nclaw:webhook\n{\"action\":\"create\"}\n```"
	result := &cli.Result{Text: text, FullText: text}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100, ThreadID: 0}, "/tmp", false)

	require.Len(t, ms.calls, 1)
	assert.NotContains(t, ms.calls[0].text, "not configured")
}

func TestProcess_RepliesWithFirstChunkOnly(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)

	long := strings.Repeat("line\n", 1200)
	p.Process(context.Background(), &cli.Result{Text: long, FullText: long}, nil, Dest{ChatID: 1, ThreadID: 2, ReplyTo: 42}, "/tmp", false)

	require.Greater(t, len(ms.calls), 1)
	assert.Equal(t, 42, ms.calls[0].replyTo)
	for _, c := range ms.calls[1:] {
		assert.Zero(t, c.replyTo)
		assert.Equal(t, 2, c.threadID)
	}
}

func longHTMLAnswer() string {
	return "<b>Report</b>\n" + strings.Repeat("row of the report\n", 1000)
}

func TestProcess_LongAnswerGoesToFile(t *testing.T) {
	ms := &mockSend{}
	var docName, docBody, docCaption string
	senders := sendfile.Senders{Doc: func(_ context.Context, _ int64, _ int, filename string, data []byte, caption string) error {
		docName, docBody, docCaption = filename, string(data), caption
		return nil
	}}
	p := New(ms.fn(), senders, true)

	text := longHTMLAnswer()
	p.Process(context.Background(), &cli.Result{Text: text, FullText: text}, nil, Dest{ChatID: 1, ReplyTo: 9}, "/tmp", false)

	require.Len(t, ms.calls, 1)
	assert.Equal(t, 9, ms.calls[0].replyTo)
	assert.True(t, strings.HasPrefix(ms.calls[0].text, "<b>Report</b>"))
	assert.Equal(t, "answer.md", docName)
	assert.True(t, strings.HasPrefix(docBody, "**Report**\nrow of the report"))
	assert.Equal(t, longAnswerCaption, docCaption)
}

func TestProcess_LongAnswerFallsBackToChunks(t *testing.T) {
	ms := &mockSend{}
	senders := sendfile.Senders{Doc: func(context.Context, int64, int, string, []byte, string) error {
		return errors.New("too big")
	}}
	p := New(ms.fn(), senders, true)

	text := longHTMLAnswer()
	p.Process(context.Background(), &cli.Result{Text: text, FullText: text}, nil, Dest{ChatID: 1, ReplyTo: 9}, "/tmp", false)

	assert.Greater(t, len(ms.calls), maxReplyChunks)
	assert.Equal(t, 9, ms.calls[0].replyTo)
	assert.Zero(t, ms.calls[1].replyTo)
}

func TestProcess_ShortAnswerStaysInChat(t *testing.T) {
	ms := &mockSend{}
	docSent := false
	p := New(ms.fn(), sendfile.Senders{Doc: func(context.Context, int64, int, string, []byte, string) error {
		docSent = true
		return nil
	}}, true)

	text := strings.Repeat("line\n", 1200)
	p.Process(context.Background(), &cli.Result{Text: text, FullText: text}, nil, Dest{ChatID: 1}, "/tmp", false)

	assert.False(t, docSent)
	assert.Len(t, ms.calls, 2)
}

func authHint(output string) string {
	if strings.Contains(output, "Failed to authenticate") {
		return "🔑 sign in again"
	}
	return ""
}

func TestProcess_FailureHintAddedToFailedRun(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)
	p.SetFailureHint(authHint)

	result := &cli.Result{Text: "Failed to authenticate: OAuth session expired"}
	p.Process(context.Background(), result, errors.New("exit status 1"), Dest{ChatID: 100}, "/tmp", false)

	require.Len(t, ms.calls, 1)
	assert.Equal(t, "Failed to authenticate: OAuth session expired\n\n🔑 sign in again", ms.calls[0].text)
}

func TestProcess_FailureHintSentEvenWhenStreamed(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)
	p.SetFailureHint(authHint)

	result := &cli.Result{Text: "Failed to authenticate"}
	p.Process(context.Background(), result, errors.New("exit status 1"), Dest{ChatID: 100}, "/tmp", true)

	require.Len(t, ms.calls, 1)
	assert.Equal(t, "🔑 sign in again", ms.calls[0].text)
}

func TestProcess_FailureHintIgnoredOnSuccess(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)
	p.SetFailureHint(authHint)

	result := &cli.Result{Text: "Failed to authenticate with Gmail, so here is what I could do", FullText: "x"}
	p.Process(context.Background(), result, nil, Dest{ChatID: 100}, "/tmp", false)

	require.Len(t, ms.calls, 1)
	assert.NotContains(t, ms.calls[0].text, "sign in again")
}

func TestProcess_ButtonsGoUnderTheLastChunkOnly(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)
	kb := buttons.Reminder()

	text := strings.Repeat("a", 4000) + "\n" + strings.Repeat("b", 100)
	p.Process(context.Background(), &cli.Result{Text: text}, nil, Dest{ChatID: 1, ReplyTo: 9, Buttons: kb}, "/tmp", false)

	require.Len(t, ms.calls, 2)
	assert.Nil(t, ms.calls[0].buttons)
	assert.Equal(t, 9, ms.calls[0].replyTo)
	assert.Equal(t, kb, ms.calls[1].buttons)
	assert.Zero(t, ms.calls[1].replyTo)
}

func TestProcess_ButtonsStayOnTheVisiblePartOfALongAnswer(t *testing.T) {
	ms := &mockSend{}
	var docs int
	p := New(ms.fn(), sendfile.Senders{Doc: func(context.Context, int64, int, string, []byte, string) error {
		docs++
		return nil
	}}, true)
	kb := buttons.Choices([]string{"Да", "Нет"})

	text := strings.Repeat(strings.Repeat("x", 100)+"\n", 200)
	p.Process(context.Background(), &cli.Result{Text: text}, nil, Dest{ChatID: 1, Buttons: kb}, "/tmp", false)

	require.Len(t, ms.calls, 1)
	assert.Equal(t, kb, ms.calls[0].buttons)
	assert.Equal(t, 1, docs)
}

func TestProcess_ButtonsBlockBecomesChoices(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)

	text := "Нашёл 4 папки кеша. Удалить?\n```nclaw:buttons\n[\"Да, удалить\", \"Нет\"]\n```"
	p.Process(context.Background(), &cli.Result{Text: text, FullText: text}, nil, Dest{ChatID: 1}, "/tmp", false)

	require.Len(t, ms.calls, 1)
	assert.Equal(t, "Нашёл 4 папки кеша. Удалить?", ms.calls[0].text)
	assert.Equal(t, buttons.Choices([]string{"Да, удалить", "Нет"}), ms.calls[0].buttons)
}

func TestProcess_CallerButtonsWinOnTheLastMessage(t *testing.T) {
	ms := &mockSend{}
	p := New(ms.fn(), sendfile.Senders{}, true)
	p.SetStreamMessages(true)

	result := &cli.Result{
		Text:     "second",
		Messages: []string{"first\n```nclaw:buttons\n[\"A\"]\n```", "second\n```nclaw:buttons\n[\"B\"]\n```"},
	}
	p.Process(context.Background(), result, nil, Dest{ChatID: 1, Buttons: buttons.Reminder()}, "/tmp", false)

	require.Len(t, ms.calls, 2)
	assert.Equal(t, buttons.Choices([]string{"A"}), ms.calls[0].buttons)
	assert.Equal(t, buttons.Reminder(), ms.calls[1].buttons)
}
