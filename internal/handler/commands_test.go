package handler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/pipeline"
)

type safeSent struct {
	mu   sync.Mutex
	msgs []string
}

func (s *safeSent) send(_ context.Context, _ pipeline.Dest, text, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, text)
	return nil
}

func (s *safeSent) all() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.msgs...)
}

type blockingClient struct {
	mockClient
	ctx     context.Context
	started chan struct{}
}

func (c *blockingClient) Context(ctx context.Context) cli.Client { c.ctx = ctx; return c }
func (c *blockingClient) Dir(string) cli.Client                  { return c }
func (c *blockingClient) SkipPermissions() cli.Client            { return c }
func (c *blockingClient) AppendSystemPrompt(string) cli.Client   { return c }
func (c *blockingClient) Continue(string) (*cli.Result, error) {
	close(c.started)
	<-c.ctx.Done()
	return &cli.Result{Text: "half"}, fmt.Errorf("claude: %w", context.Cause(c.ctx))
}

type blockingProvider struct {
	mockProvider
	client *blockingClient
}

func (p *blockingProvider) NewClient() cli.Client { return p.client }

func commandUpdate(text string) *models.Update {
	return &models.Update{Message: &models.Message{Text: text, Chat: models.Chat{ID: 100}}}
}

func TestParseCommand(t *testing.T) {
	cases := []struct {
		text, name   string
		forMe, isCmd bool
	}{
		{"/stop", "stop", true, true},
		{"/stop@MyBot", "stop", true, true},
		{"/STOP@mybot please", "stop", true, true},
		{"/stop@OtherBot", "stop", false, true},
		{"/stopx", "stopx", true, true},
		{"hi /stop", "", false, false},
		{"", "", false, false},
	}
	for _, tc := range cases {
		name, forMe, ok := parseCommand(tc.text, "MyBot")
		assert.Equal(t, tc.isCmd, ok, tc.text)
		assert.Equal(t, tc.name, name, tc.text)
		assert.Equal(t, tc.forMe, forMe, tc.text)
	}
}

func TestMatchCommand(t *testing.T) {
	h := &Handler{BotUsername: "MyBot"}
	assert.True(t, h.MatchCommand(commandUpdate("/stop")))
	assert.True(t, h.MatchCommand(commandUpdate("/status@MyBot")))
	assert.True(t, h.MatchCommand(commandUpdate("/new")))
	assert.False(t, h.MatchCommand(commandUpdate("/stop@OtherBot")))
	assert.False(t, h.MatchCommand(commandUpdate("/start")))
	assert.False(t, h.MatchCommand(commandUpdate("please /stop")))
	assert.False(t, h.MatchCommand(&models.Update{}))
}

func TestDefault_IgnoresCommandsForOtherBots(t *testing.T) {
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	h.BotUsername = "MyBot"

	h.Default(context.Background(), nil, commandUpdate("/help@OtherBot"))

	assert.Equal(t, chatqueue.Snapshot{}, h.Queue.Snapshot(testKey))
}

func TestStopCommand_CancelsRunningRequest(t *testing.T) {
	client := &blockingClient{started: make(chan struct{})}
	sent := &safeSent{}
	h := newTestHandler(t, &blockingProvider{client: client}, sent.send)
	h.Send = sent.send

	h.Default(context.Background(), nil, commandUpdate("long job"))
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not start")
	}
	h.Default(context.Background(), nil, commandUpdate("queued follow-up"))
	h.Command(context.Background(), nil, commandUpdate("/stop"))
	waitQueue(t, h)

	require.Eventually(t, func() bool { return len(sent.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Contains(t, sent.all()[0], "⏹ Stopped your request after")
	assert.Contains(t, sent.all()[0], "Dropped 1 queued message(s).")
}

func TestStopCommand_Idle(t *testing.T) {
	sent := &safeSent{}
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	h.Send = sent.send

	h.Command(context.Background(), nil, commandUpdate("/stop"))

	require.Eventually(t, func() bool { return len(sent.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "Nothing to stop.", sent.all()[0])
}

func TestNewCommand_StartsFreshConversation(t *testing.T) {
	client := &mockClient{contResult: &cli.Result{Text: "ok"}, askResult: &cli.Result{Text: "fresh"}}
	sent := &safeSent{}
	h := newTestHandler(t, &mockProvider{client: client}, sent.send)
	h.Send = sent.send

	h.Command(context.Background(), nil, commandUpdate("/new"))
	waitQueue(t, h)
	_, err := os.Stat(filepath.Join(h.Invoker.ChatDir(100, 0), ".nclaw-new-session"))
	require.NoError(t, err)

	h.Default(context.Background(), nil, commandUpdate("hello again"))
	waitQueue(t, h)

	assert.Equal(t, []string{"🆕 New conversation started.", "fresh"}, sent.all())
}

func TestStatusText(t *testing.T) {
	idle := statusText(&chatqueue.Snapshot{}, 0, false, 0, "claude")
	assert.Equal(t, "💤 Idle.\nBackend: claude.", idle)

	busy := statusText(&chatqueue.Snapshot{
		Running: true, Kind: chatqueue.KindUser, Batch: 3, Started: time.Now().Add(-90 * time.Second),
		PendingUser: 2, PendingJobs: 1,
	}, 5<<20, true, 10<<20, "claude")
	assert.Contains(t, busy, "▶️ Running your request for 1m30s. (3 messages merged)")
	assert.Contains(t, busy, "Queued: 2 message(s), 1 task/webhook run(s).")
	assert.Contains(t, busy, "Session: 5.0 MB of 10.0 MB (50%).")
}

func TestStatusCommand(t *testing.T) {
	sent := &safeSent{}
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	h.Send = sent.send

	h.Command(context.Background(), nil, commandUpdate("/status"))

	require.Eventually(t, func() bool { return len(sent.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "💤 Idle.\nBackend: mock.", sent.all()[0])
}

func TestHumanBytes(t *testing.T) {
	assert.Equal(t, "512 B", humanBytes(512))
	assert.Equal(t, "1.5 KB", humanBytes(1536))
	assert.Equal(t, "2.0 MB", humanBytes(2<<20))
}

func TestShutdownText(t *testing.T) {
	assert.Equal(t, "♻️ The bot is restarting and your request was interrupted. Please send it again in a minute — the conversation is kept.",
		shutdownText(chatqueue.Interrupted{Running: true, Kind: chatqueue.KindUser}))
	assert.Equal(t, "♻️ The bot is restarting; a scheduled task in progress was interrupted. 2 queued message(s) were not processed.",
		shutdownText(chatqueue.Interrupted{Running: true, Kind: chatqueue.KindScheduled, DroppedUser: 2}))
	assert.Equal(t, "♻️ The bot is restarting. 1 queued message(s) were not processed.",
		shutdownText(chatqueue.Interrupted{DroppedUser: 1}))
	assert.Empty(t, shutdownText(chatqueue.Interrupted{DroppedJobs: 3}))
}

func TestShutdown_InterruptsRunAndNotifiesChat(t *testing.T) {
	client := &blockingClient{started: make(chan struct{})}
	sent := &safeSent{}
	h := newTestHandler(t, &blockingProvider{client: client}, sent.send)
	h.Send = sent.send

	h.Default(context.Background(), nil, commandUpdate("long job"))
	select {
	case <-client.started:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not start")
	}

	h.NotifyShutdown(h.Queue.Close())
	waitQueue(t, h)

	require.Len(t, sent.all(), 1)
	assert.Contains(t, sent.all()[0], "your request was interrupted")
}
