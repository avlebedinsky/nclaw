package handler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nickalie/nclaw/internal/buttons"
	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/model"
	"github.com/nickalie/nclaw/internal/pipeline"
)

type edited struct {
	msgID    int
	text     string
	entities []models.MessageEntity
	kb       buttons.Keyboard
}

type fakeButtons struct {
	mu      sync.Mutex
	answers []string
	edits   []edited
}

func (f *fakeButtons) Answer(_ context.Context, _ string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, text)
	return nil
}

func (f *fakeButtons) Edit(_ context.Context, msg *models.Message, text string, entities []models.MessageEntity, kb buttons.Keyboard) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, edited{msgID: msg.ID, text: text, entities: entities, kb: kb})
	return nil
}

func (f *fakeButtons) waitAnswer(t *testing.T) string {
	t.Helper()
	require.Eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.answers) > 0
	}, 5*time.Second, 10*time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.answers[0]
}

func (f *fakeButtons) waitEdit(t *testing.T) edited {
	t.Helper()
	require.Eventually(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.edits) > 0
	}, 5*time.Second, 10*time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.edits[0]
}

type snoozed struct {
	chatID   int64
	threadID int
	text     string
	after    time.Duration
}

type fakeTasks struct {
	snoozes   []snoozed
	at        time.Time
	err       error
	tasks     []model.ScheduledTask
	actions   []string
	actionErr error
}

func (f *fakeTasks) Snooze(chatID int64, threadID int, text string, after time.Duration) (time.Time, error) {
	f.snoozes = append(f.snoozes, snoozed{chatID, threadID, text, after})
	return f.at, f.err
}

func (f *fakeTasks) ChatTasks(int64, int) ([]model.ScheduledTask, error) {
	return f.tasks, f.err
}

func (f *fakeTasks) TaskAction(action, taskID string, _ int64, _ int) error {
	f.actions = append(f.actions, action+":"+taskID)
	return f.actionErr
}

func newButtonHandler(t *testing.T) (*Handler, *fakeButtons, *fakeTasks) {
	t.Helper()
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)
	fb, ft := &fakeButtons{}, &fakeTasks{}
	h.Buttons, h.Tasks = fb, ft
	return h, fb, ft
}

func press(msg *models.Message, data string) *models.Update {
	return &models.Update{CallbackQuery: &models.CallbackQuery{
		ID: "q1", Data: data, Message: models.MaybeInaccessibleMessage{Message: msg},
	}}
}

func reminder() *models.Message {
	return &models.Message{
		ID: 42, Text: "⏰ Позвонить стоматологу", Chat: models.Chat{ID: 100}, MessageThreadID: 7,
		Entities: []models.MessageEntity{{Type: models.MessageEntityTypeBold, Offset: 3, Length: 9}},
	}
}

func TestButton_DoneClosesTheReminder(t *testing.T) {
	h, fb, _ := newButtonHandler(t)
	require.True(t, h.MatchButton(press(reminder(), "d")))

	h.Button(context.Background(), nil, press(reminder(), "d"))

	assert.Equal(t, "Done", fb.waitAnswer(t))
	e := fb.waitEdit(t)
	assert.Equal(t, 42, e.msgID)
	assert.Equal(t, "⏰ Позвонить стоматологу\n\n✅ Done", e.text)
	assert.Equal(t, reminder().Entities, e.entities)
	assert.Nil(t, e.kb)
}

func TestButton_SnoozeSchedulesTheSameText(t *testing.T) {
	h, fb, ft := newButtonHandler(t)
	ft.at = time.Now().Add(time.Hour)

	h.Button(context.Background(), nil, press(reminder(), "z:60"))

	require.Len(t, ft.snoozes, 1)
	assert.Equal(t, snoozed{100, 7, "⏰ Позвонить стоматологу", time.Hour}, ft.snoozes[0])
	want := "⏰ Snoozed until " + when(ft.at, time.Now())
	assert.Equal(t, want, fb.waitAnswer(t))
	assert.Equal(t, "⏰ Позвонить стоматологу\n\n"+want, fb.waitEdit(t).text)
}

func TestButton_SnoozeFailureKeepsTheButtons(t *testing.T) {
	h, fb, ft := newButtonHandler(t)
	ft.err = errors.New("db is down")

	h.Button(context.Background(), nil, press(reminder(), "z:15"))

	assert.Equal(t, "Could not snooze: db is down", fb.waitAnswer(t))
	fb.mu.Lock()
	defer fb.mu.Unlock()
	assert.Empty(t, fb.edits)
}

func TestButton_InaccessibleMessageOrUnknownData(t *testing.T) {
	h, fb, ft := newButtonHandler(t)
	old := &models.Update{CallbackQuery: &models.CallbackQuery{ID: "q", Data: "z:15", Message: models.MaybeInaccessibleMessage{
		InaccessibleMessage: &models.InaccessibleMessage{Chat: models.Chat{ID: 100}, MessageID: 1},
	}}}

	h.Button(context.Background(), nil, old)
	h.Button(context.Background(), nil, press(reminder(), "zz"))

	require.Eventually(t, func() bool {
		fb.mu.Lock()
		defer fb.mu.Unlock()
		return len(fb.answers) == 2
	}, 5*time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{staleButton, staleButton}, fb.answers)
	assert.Empty(t, ft.snoozes)
}

func TestWhen(t *testing.T) {
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	assert.Equal(t, "13:45", when(time.Date(2026, 10, 4, 13, 45, 0, 0, time.UTC), now))
	assert.Equal(t, "Mon 5 Oct 12:00", when(time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC), now))
}

func TestAllowChat_ButtonPresses(t *testing.T) {
	viper.Set("telegram.whitelist_chat_ids", "100")
	defer viper.Reset()

	var passed int
	next := AllowChat(func(context.Context, *bot.Bot, *models.Update) { passed++ })
	next(context.Background(), nil, press(reminder(), "d"))
	stranger := reminder()
	stranger.Chat.ID = 999
	next(context.Background(), nil, press(stranger, "d"))

	assert.Equal(t, 1, passed)
}

func question() *models.Message {
	kb := buttons.Markup(buttons.Choices([]string{"Да, удалить", "Нет"}))
	return &models.Message{
		ID: 77, Text: "Нашёл 4 папки кеша. Удалить?", Chat: models.Chat{ID: 100}, ReplyMarkup: &kb,
	}
}

func TestButton_ChoiceGoesToTheAgentAsTheUsersReply(t *testing.T) {
	client := &mockClient{}
	h := newTestHandler(t, &mockProvider{client: client}, nil)
	fb := &fakeButtons{}
	h.Buttons = fb

	h.Button(context.Background(), nil, press(question(), "c:0"))

	assert.Equal(t, "Да, удалить", fb.waitAnswer(t))
	e := fb.waitEdit(t)
	assert.Equal(t, "Нашёл 4 папки кеша. Удалить?\n\n→ Да, удалить", e.text)
	assert.Nil(t, e.kb)
	waitQueue(t, h)
	assert.Contains(t, client.lastQuery, "[Pressed a button under your message \"Нашёл 4 папки кеша. Удалить?\"]\n\nДа, удалить")
}

func TestButton_ChoiceInAGroupNamesWhoPressed(t *testing.T) {
	client := &mockClient{}
	h := newTestHandler(t, &mockProvider{client: client}, nil)
	fb := &fakeButtons{}
	h.Buttons = fb
	msg := question()
	msg.Chat.ID = -100500
	q := press(msg, "c:1")
	q.CallbackQuery.From = models.User{FirstName: "Анна"}

	h.Button(context.Background(), nil, q)

	assert.Equal(t, "Нет", fb.waitAnswer(t))
	waitQueue(t, h)
	assert.Contains(t, client.lastQuery, "[From: Анна]\n[Pressed a button")
}

func TestButton_ChoiceWithoutKeyboardIsStale(t *testing.T) {
	h, fb, _ := newButtonHandler(t)
	msg := question()
	msg.ReplyMarkup = nil

	h.Button(context.Background(), nil, press(msg, "c:0"))

	assert.Equal(t, staleButton, fb.waitAnswer(t))
}

type interruptibleClient struct {
	mockClient
	mu      sync.Mutex
	ctx     context.Context
	queries []string
	started chan string
}

func (c *interruptibleClient) Context(ctx context.Context) cli.Client {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ctx = ctx
	return c
}
func (c *interruptibleClient) Dir(string) cli.Client                { return c }
func (c *interruptibleClient) SkipPermissions() cli.Client          { return c }
func (c *interruptibleClient) AppendSystemPrompt(string) cli.Client { return c }
func (c *interruptibleClient) Continue(query string) (*cli.Result, error) {
	c.mu.Lock()
	ctx := c.ctx
	c.queries = append(c.queries, query)
	first := len(c.queries) == 1
	c.mu.Unlock()
	c.started <- query
	if first {
		<-ctx.Done()
		return &cli.Result{Text: "half done"}, context.Cause(ctx)
	}
	return &cli.Result{Text: "done with the new request"}, nil
}

type interruptibleProvider struct {
	mockProvider
	client *interruptibleClient
}

func (p *interruptibleProvider) NewClient() cli.Client { return p.client }

func startLongRun(t *testing.T) (*Handler, *interruptibleClient, *safeSent, *fakeButtons) {
	t.Helper()
	client := &interruptibleClient{started: make(chan string, 4)}
	sent := &safeSent{}
	h := newTestHandler(t, &interruptibleProvider{client: client}, sent.send)
	h.Send = sent.send
	fb := &fakeButtons{}
	h.Buttons = fb
	h.Default(context.Background(), nil, chatMessage("collect a long report"))
	<-client.started
	return h, client, sent, fb
}

func statusMessage() *models.Message {
	return &models.Message{ID: 9, Text: "🔧 Bash: make\nstep 1", Chat: models.Chat{ID: 100}}
}

func TestButton_AnswerNewInterruptsAndAnswersTheNewMessage(t *testing.T) {
	h, client, sent, fb := startLongRun(t)
	h.Default(context.Background(), nil, chatMessage("no, just the summary"))
	require.Eventually(t, func() bool { return h.Queue.Snapshot(testKey).PendingUser == 1 }, 5*time.Second, 10*time.Millisecond)

	h.Button(context.Background(), nil, press(statusMessage(), "n"))

	assert.Equal(t, "⏭ Answering your new messages.", fb.waitAnswer(t))
	assert.Contains(t, <-client.started, "no, just the summary")
	waitQueue(t, h)
	assert.Equal(t, []string{"done with the new request"}, sent.all())
}

func TestButton_AnswerNewWithoutNewMessagesDoesNothing(t *testing.T) {
	h, _, _, fb := startLongRun(t)

	h.Button(context.Background(), nil, press(statusMessage(), "n"))

	assert.Equal(t, "No new messages to answer.", fb.waitAnswer(t))
	assert.True(t, h.Queue.Snapshot(testKey).Running)
	h.Queue.Stop(testKey)
	waitQueue(t, h)
}

func TestButton_StopStopsTheRunAndDropsTheQueue(t *testing.T) {
	h, _, sent, fb := startLongRun(t)
	h.Default(context.Background(), nil, chatMessage("and this"))

	h.Button(context.Background(), nil, press(statusMessage(), "s"))

	assert.Contains(t, fb.waitAnswer(t), "⏹ Stopped your request after")
	waitQueue(t, h)
	assert.Empty(t, sent.all())
}

type sentMessage struct {
	dest pipeline.Dest
	text string
}

func captureSends(h *Handler) chan sentMessage {
	sends := make(chan sentMessage, 4)
	h.Send = func(_ context.Context, dest pipeline.Dest, text, _ string) error {
		sends <- sentMessage{dest, text}
		return nil
	}
	return sends
}

func nextSend(t *testing.T, sends chan sentMessage) sentMessage {
	t.Helper()
	select {
	case m := <-sends:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("nothing was sent")
		return sentMessage{}
	}
}

func TestTasksCommand_ListsTheTasksWithButtons(t *testing.T) {
	h, _, ft := newButtonHandler(t)
	sends := captureSends(h)
	next := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	ft.tasks = []model.ScheduledTask{
		{ID: "t1", Prompt: "Утренняя\nсводка", ScheduleType: model.ScheduleCron, ScheduleValue: "0 9 * * *", Status: model.StatusActive, NextRun: &next},
		{ID: "t2", Prompt: "Проверить почту", ScheduleType: model.ScheduleInterval, ScheduleValue: "2h", Status: model.StatusActive},
		{ID: "t3", Prompt: "⏰ Полить цветы", ScheduleType: model.ScheduleOnce, ScheduleValue: "2026-10-05T09:00:00", Status: model.StatusPaused, NextRun: &next},
	}
	require.True(t, h.MatchCommand(commandUpdate("/tasks")))

	h.Command(context.Background(), nil, commandUpdate("/tasks"))

	m := nextSend(t, sends)
	assert.Equal(t, "Scheduled tasks here:\n"+
		"1. Утренняя сводка — cron 0 9 * * *, next Mon 5 Oct 09:00\n"+
		"2. Проверить почту — every 2h\n"+
		"3. ⏰ Полить цветы — once at Mon 5 Oct 09:00, paused", m.text)
	assert.Equal(t, buttons.Keyboard{buttons.Task(1, "t1", false), buttons.Task(2, "t2", false), buttons.Task(3, "t3", true)}, m.dest.Buttons)
	assert.Equal(t, int64(100), m.dest.ChatID)
}

func TestTasksCommand_NoTasks(t *testing.T) {
	h, _, _ := newButtonHandler(t)
	sends := captureSends(h)

	h.Command(context.Background(), nil, commandUpdate("/tasks"))

	m := nextSend(t, sends)
	assert.Equal(t, "No scheduled tasks here.", m.text)
	assert.Nil(t, m.dest.Buttons)
}

func TestTasksCommand_UnavailableWithoutScheduler(t *testing.T) {
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)

	assert.False(t, h.MatchCommand(commandUpdate("/tasks")))
}

func taskList() *models.Message {
	return &models.Message{
		ID: 5, Text: "Scheduled tasks here:\n1. https://example.com", Chat: models.Chat{ID: 100},
		Entities: []models.MessageEntity{{Type: models.MessageEntityTypeURL, Offset: 25, Length: 19}},
	}
}

func TestButton_TaskActionAppliesAndRedrawsTheList(t *testing.T) {
	h, fb, ft := newButtonHandler(t)
	ft.tasks = []model.ScheduledTask{{ID: "t1", Prompt: "Полить цветы", ScheduleType: model.ScheduleInterval, ScheduleValue: "24h", Status: model.StatusPaused}}

	h.Button(context.Background(), nil, press(taskList(), "t:p:t1"))

	assert.Equal(t, "⏸ Paused", fb.waitAnswer(t))
	assert.Equal(t, []string{"pause:t1"}, ft.actions)
	e := fb.waitEdit(t)
	assert.Equal(t, 5, e.msgID)
	assert.Equal(t, "Scheduled tasks here:\n1. Полить цветы — every 24h, paused", e.text)
	assert.Nil(t, e.entities)
	assert.Equal(t, buttons.Keyboard{buttons.Task(1, "t1", true)}, e.kb)
}

func TestButton_TaskActionFailureSaysWhyAndShowsTheCurrentList(t *testing.T) {
	h, fb, ft := newButtonHandler(t)
	ft.actionErr = errors.New("task not found: t9")

	h.Button(context.Background(), nil, press(taskList(), "t:c:t9"))

	assert.Equal(t, "Could not cancel: task not found: t9", fb.waitAnswer(t))
	e := fb.waitEdit(t)
	assert.Equal(t, "No scheduled tasks here.", e.text)
	assert.Nil(t, e.kb)
}
