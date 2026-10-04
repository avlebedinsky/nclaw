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
	"github.com/nickalie/nclaw/internal/model"
)

type edited struct {
	msgID int
	text  string
	kb    buttons.Keyboard
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

func (f *fakeButtons) Edit(_ context.Context, msg *models.Message, text string, kb buttons.Keyboard) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, edited{msgID: msg.ID, text: text, kb: kb})
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
	snoozes []snoozed
	at      time.Time
	err     error
	tasks   []model.ScheduledTask
	actions []string
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
	return f.err
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
	return &models.Message{ID: 42, Text: "⏰ Позвонить стоматологу", Chat: models.Chat{ID: 100}, MessageThreadID: 7}
}

func TestButton_DoneClosesTheReminder(t *testing.T) {
	h, fb, _ := newButtonHandler(t)
	require.True(t, h.MatchButton(press(reminder(), "d")))

	h.Button(context.Background(), nil, press(reminder(), "d"))

	assert.Equal(t, "Done", fb.waitAnswer(t))
	e := fb.waitEdit(t)
	assert.Equal(t, 42, e.msgID)
	assert.Equal(t, "⏰ Позвонить стоматологу\n\n✅ Done", e.text)
	assert.Nil(t, e.kb)
}

func TestButton_SnoozeSchedulesTheSameText(t *testing.T) {
	h, fb, ft := newButtonHandler(t)
	ft.at = time.Now().Add(time.Hour)

	h.Button(context.Background(), nil, press(reminder(), "z:60"))

	require.Len(t, ft.snoozes, 1)
	assert.Equal(t, snoozed{100, 7, "⏰ Позвонить стоматологу", time.Hour}, ft.snoozes[0])
	want := "⏰ Snoozed until " + ft.at.Format("15:04")
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
