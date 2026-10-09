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

	"github.com/nickalie/nclaw/internal/buttons"
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
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	h.BotUsername = "MyBot"
	assert.True(t, h.MatchCommand(commandUpdate("/stop")))
	assert.True(t, h.MatchCommand(commandUpdate("/status@MyBot")))
	assert.True(t, h.MatchCommand(commandUpdate("/new")))
	assert.False(t, h.MatchCommand(commandUpdate("/stop@OtherBot")))
	assert.False(t, h.MatchCommand(commandUpdate("/unknown")))
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
	assert.Contains(t, sent.all()[0], "⏹ Остановлено через")
	assert.Contains(t, sent.all()[0], "Убрано из очереди сообщений: 1.")
}

func TestStopCommand_Idle(t *testing.T) {
	sent := &safeSent{}
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	h.Send = sent.send

	h.Command(context.Background(), nil, commandUpdate("/stop"))

	require.Eventually(t, func() bool { return len(sent.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "Останавливать нечего.", sent.all()[0])
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

	assert.Equal(t, []string{"🆕 Начат новый разговор.", "fresh"}, sent.all())
}

func TestStatusText(t *testing.T) {
	idle := statusText(&chatqueue.Snapshot{}, 0, false, 0, "claude")
	assert.Equal(t, "💤 Сейчас ничего не выполняется.\nБэкенд: claude.", idle)

	busy := statusText(&chatqueue.Snapshot{
		Running: true, Kind: chatqueue.KindUser, Batch: 3, Started: time.Now().Add(-90 * time.Second),
		PendingUser: 2, PendingJobs: 1,
	}, 5<<20, true, 10<<20, "claude")
	assert.Contains(t, busy, "▶️ Выполняется ваш запрос, уже 1 мин 30 с. Сообщений в одном запросе: 3.")
	assert.Contains(t, busy, "В очереди: сообщений — 2, задач и вебхуков — 1.")
	assert.Contains(t, busy, "Сессия: 5.0 МБ из 10.0 МБ (50%).")
}

func TestStatusCommand(t *testing.T) {
	sent := &safeSent{}
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	h.Send = sent.send

	h.Command(context.Background(), nil, commandUpdate("/status"))

	require.Eventually(t, func() bool { return len(sent.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "💤 Сейчас ничего не выполняется.\nБэкенд: mock.", sent.all()[0])
}

func TestStatusCommand_ShowsSignInExpiry(t *testing.T) {
	sent := &safeSent{}
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	h.Send = sent.send
	h.AuthStatus = func() string { return "Claude sign-in: valid until Fri 9 Oct 18:06 MSK (5 days left)." }

	h.Command(context.Background(), nil, commandUpdate("/status"))

	require.Eventually(t, func() bool { return len(sent.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "💤 Сейчас ничего не выполняется.\nБэкенд: mock.\nClaude sign-in: valid until Fri 9 Oct 18:06 MSK (5 days left).", sent.all()[0])
}

func TestHumanBytes(t *testing.T) {
	assert.Equal(t, "512 Б", humanBytes(512))
	assert.Equal(t, "1.5 КБ", humanBytes(1536))
	assert.Equal(t, "2.0 МБ", humanBytes(2<<20))
}

func TestShutdownText(t *testing.T) {
	assert.Equal(t, "♻️ Бот перезапускается, ваш запрос прерван. Отправьте его ещё раз через минуту — разговор сохранится.",
		shutdownText(chatqueue.Interrupted{Running: true, Kind: chatqueue.KindUser}))
	assert.Equal(t, "♻️ Бот перезапускается, прервано: задача по расписанию. Не обработаны сообщения из очереди: 2.",
		shutdownText(chatqueue.Interrupted{Running: true, Kind: chatqueue.KindScheduled, DroppedUser: 2}))
	assert.Equal(t, "♻️ Бот перезапускается. Не обработаны сообщения из очереди: 1.",
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
	assert.Contains(t, sent.all()[0], "ваш запрос прерван")
}

func writeSkill(t *testing.T, dir, name, description string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, name), 0o755))
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, name, "SKILL.md"), []byte(body), 0o644))
}

func TestSkillsCommand_ListsTopicAndGlobalSkills(t *testing.T) {
	sent := &safeSent{}
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	h.Send = sent.send
	global, extra := t.TempDir(), t.TempDir()
	writeSkill(t, global, "gmail", "Read and send mail. Use when asked about email.")
	writeSkill(t, extra, "gmail", "Duplicate from a second skills dir.")
	writeSkill(t, global, "schedule", "Schedule tasks.")
	var gotDir string
	h.SkillDirs = func(workDir string) (string, []string) {
		gotDir = workDir
		local := filepath.Join(workDir, ".claude", "skills")
		writeSkill(t, local, "topic-books", "Книги в этом топике. Подробности дальше.")
		return local, []string{global, extra}
	}
	update := commandUpdate("/skills")
	update.Message.MessageThreadID = 546

	h.Command(context.Background(), nil, update)

	require.Eventually(t, func() bool { return len(sent.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, h.Invoker.ChatDir(100, 546), gotDir)
	assert.Equal(t, "🧩 Скиллы этого топика:\n• topic-books — Книги в этом топике.\n\n"+
		"🧩 Общие скиллы (2):\n• gmail — Read and send mail.\n• schedule — Schedule tasks.", sent.all()[0])
}

func TestSkillsCommand_NothingInstalled(t *testing.T) {
	sent := &safeSent{}
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	h.Send = sent.send
	h.SkillDirs = func(string) (string, []string) { return "", []string{t.TempDir()} }

	h.Command(context.Background(), nil, commandUpdate("/skills"))

	require.Eventually(t, func() bool { return len(sent.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, "Скиллов нет.", sent.all()[0])
}

type memoryProvider struct {
	mockProvider
	memDir string
}

func (p *memoryProvider) MemoryFile() string                   { return "CLAUDE.md" }
func (p *memoryProvider) AutoMemoryDir(string) (string, error) { return p.memDir, nil }
func (p *memoryProvider) ClearAutoMemory(string) error         { return os.RemoveAll(p.memDir) }

func TestMemoryCommand_ShowsWhatTheTopicRemembersAndClearsIt(t *testing.T) {
	mem := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(mem, "MEMORY.md"), []byte("- [Кормление](feeding.md) — расписание\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mem, "feeding.md"), []byte("утром и вечером"), 0o644))
	h := newTestHandler(t, &memoryProvider{mockProvider: mockProvider{client: &mockClient{}}, memDir: mem}, nil)
	fb := &fakeButtons{}
	h.Buttons = fb
	sends := captureSends(h)
	require.NoError(t, os.MkdirAll(h.Invoker.ChatDir(100, 7), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(h.Invoker.ChatDir(100, 0), "CLAUDE.md"), []byte("# Группа\nтопики"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(h.Invoker.ChatDir(100, 7), "CLAUDE.md"), []byte("# Топик"), 0o644))
	update := commandUpdate("/memory")
	update.Message.MessageThreadID = 7

	h.Command(context.Background(), nil, update)

	m := nextSend(t, sends)
	assert.Equal(t, "🧠 Память этого разговора:\n- [Кормление](feeding.md) — расписание\nФайлы заметок: feeding.md\n\n"+
		"📌 Общее для всех топиков группы (CLAUDE.md):\n# Группа\nтопики\n\n"+
		"📌 Заметки этого топика (CLAUDE.md):\n# Топик", m.text)
	assert.Equal(t, buttons.Memory(), m.dest.Buttons)

	msg := &models.Message{ID: 4, Chat: models.Chat{ID: 100}, MessageThreadID: 7}
	h.Button(context.Background(), nil, press(msg, "forget"))
	assert.Equal(t, "🧹 Память разговора очищена", fb.waitAnswer(t))
	e := fb.waitEdit(t)
	assert.NotContains(t, e.text, "Память этого разговора")
	assert.Contains(t, e.text, "Заметки этого топика")
	assert.Nil(t, e.kb)
}

func TestMemoryCommand_NothingRemembered(t *testing.T) {
	h := newTestHandler(t, &memoryProvider{mockProvider: mockProvider{client: &mockClient{}}, memDir: t.TempDir()}, nil)
	sends := captureSends(h)

	h.Command(context.Background(), nil, commandUpdate("/memory"))

	m := nextSend(t, sends)
	assert.Equal(t, "Здесь пока ничего не запомнено.", m.text)
	assert.Nil(t, m.dest.Buttons)
}

func TestMemoryCommand_UnavailableWithoutMemory(t *testing.T) {
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)

	assert.False(t, h.MatchCommand(commandUpdate("/memory")))
}

type contextProvider struct {
	modelsProvider
}

func (p *contextProvider) ContextUsage(string) (cli.ContextUsage, bool) {
	return cli.ContextUsage{Tokens: 156_300, CompactAt: 400_000, Model: "claude-opus-5-5"}, true
}
func (p *contextProvider) CompactPrompt() string { return "/compact" }

func TestStatusCommand_ShowsContextModelAndButtons(t *testing.T) {
	client := &mockClient{contResult: &cli.Result{}}
	h := newTestHandler(t, &contextProvider{modelsProvider{mockProvider{client: client}}}, nil)
	sends := captureSends(h)

	h.Command(context.Background(), nil, commandUpdate("/status"))

	m := nextSend(t, sends)
	assert.Equal(t, "💤 Сейчас ничего не выполняется.\nБэкенд: mock.\n"+
		"Контекст: 156K токенов из 400K до автосжатия (39%).\n"+
		"Модель: по умолчанию, последний ответ — claude-opus-5-5.", m.text)
	assert.Equal(t, buttons.Conversation(), m.dest.Buttons)
}

func TestStatusButtons_CompactAndNewConversation(t *testing.T) {
	client := &mockClient{contResult: &cli.Result{}}
	h := newTestHandler(t, &contextProvider{modelsProvider{mockProvider{client: client}}}, nil)
	fb := &fakeButtons{}
	h.Buttons = fb
	sends := captureSends(h)
	msg := &models.Message{ID: 8, Chat: models.Chat{ID: 100}}

	h.Button(context.Background(), nil, press(msg, "compact"))
	assert.Equal(t, "🗜 Сжимаю разговор…", fb.waitAnswer(t))
	assert.Equal(t, "🗜 Разговор сжат: было 156K токенов, стало 156K.", nextSend(t, sends).text)
	assert.Equal(t, "/compact", client.lastQuery)

	h.Button(context.Background(), nil, press(msg, "reset"))
	assert.Equal(t, "🆕 Начат новый разговор.", nextSend(t, sends).text)
}

func TestHelpCommand_ListsOnlyWhatWorksHere(t *testing.T) {
	sent := &safeSent{}
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	h.Send = sent.send
	require.True(t, h.MatchCommand(commandUpdate("/start")))

	h.Command(context.Background(), nil, commandUpdate("/help"))

	require.Eventually(t, func() bool { return len(sent.all()) == 1 }, 5*time.Second, 10*time.Millisecond)
	text := sent.all()[0]
	assert.Contains(t, text, "Команды:\n/stop — остановить ответ и очистить очередь\n/new — начать новый разговор\n")
	assert.Contains(t, text, "/help — что умеет бот\n\nКнопки:")
	assert.NotContains(t, text, "/tasks")
	assert.NotContains(t, text, "/model")
	assert.NotContains(t, text, "/memory")
}
