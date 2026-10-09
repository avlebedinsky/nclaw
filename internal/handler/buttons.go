package handler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/nickalie/nclaw/internal/buttons"
	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/model"
	"github.com/nickalie/nclaw/internal/ru"
)

const staleButton = "Эта кнопка больше не работает."

// ButtonAPI answers presses on inline buttons and edits the messages that carry them.
type ButtonAPI interface {
	Answer(ctx context.Context, queryID, text string) error
	Edit(ctx context.Context, msg *models.Message, text string, entities []models.MessageEntity, kb buttons.Keyboard) error
}

// TaskManager is the part of the scheduler that buttons and /tasks act on.
type TaskManager interface {
	Snooze(chatID int64, threadID int, text string, after time.Duration) (time.Time, error)
	ChatTasks(chatID int64, threadID int) ([]model.ScheduledTask, error)
	TaskAction(action, taskID string, chatID int64, threadID int) error
}

// MatchButton reports whether the update is a press on one of the bot's inline buttons.
func (h *Handler) MatchButton(update *models.Update) bool {
	return update.CallbackQuery != nil
}

// Button handles a press on one of the bot's inline buttons.
func (h *Handler) Button(_ context.Context, _ *bot.Bot, update *models.Update) {
	q := update.CallbackQuery
	toast := staleButton
	if msg := q.Message.Message; msg != nil {
		toast = h.press(q, msg, buttons.Parse(q.Data))
	}
	go h.answer(q.ID, toast)
}

func (h *Handler) press(q *models.CallbackQuery, msg *models.Message, p buttons.Press) string {
	key := chatqueue.Key{ChatID: msg.Chat.ID, ThreadID: msg.MessageThreadID}
	switch p.Kind {
	case buttons.Choice:
		return h.choose(q, msg, key)
	case buttons.Done:
		go h.closeButtons(msg, "✅ Готово")
		return "Готово"
	case buttons.Snooze:
		return h.snooze(msg, key, p.After)
	case buttons.Stop:
		return stopText(h.Queue.Stop(key))
	case buttons.AnswerNew:
		return h.answerNew(key)
	case buttons.TaskAction:
		return h.taskButton(msg, key, p)
	default:
		return staleButton
	}
}

var (
	taskToasts = map[string]string{buttons.TaskPause: "⏸ На паузе", buttons.TaskResume: "▶️ Возобновлена", buttons.TaskCancel: "🗑 Удалена"}
	taskVerbs  = map[string]string{buttons.TaskPause: "приостановить", buttons.TaskResume: "возобновить", buttons.TaskCancel: "удалить"}
)

func (h *Handler) taskButton(msg *models.Message, key chatqueue.Key, p buttons.Press) string {
	if h.Tasks == nil {
		return staleButton
	}
	toast := taskToasts[p.Action]
	if err := h.Tasks.TaskAction(p.Action, p.TaskID, key.ChatID, key.ThreadID); err != nil {
		toast = "Не удалось " + taskVerbs[p.Action] + ": " + err.Error()
	}
	text, kb := h.taskList(key)
	go h.edit(msg, text, nil, kb)
	return toast
}

func (h *Handler) answerNew(key chatqueue.Key) string {
	if h.Queue.Snapshot(key).PendingUser == 0 {
		return "Новых сообщений нет."
	}
	if !h.Queue.Interrupt(key) {
		return "Прерывать нечего."
	}
	return "⏭ Отвечаю на новые сообщения."
}

func (h *Handler) choose(q *models.CallbackQuery, msg *models.Message, key chatqueue.Key) string {
	label := buttons.FromMarkup(msg.ReplyMarkup).Label(q.Data)
	if label == "" {
		return staleButton
	}
	in := Inbound{msgID: msg.ID, sender: pressedBy(msg, &q.From), text: choicePrompt(msg.Text, label)}
	if err := h.Queue.Enqueue(key, in, messageSettle); err != nil {
		log.Printf("handler: chat=%d thread=%d choice not queued: %v", key.ChatID, key.ThreadID, err)
		return "Не удалось отправить: " + err.Error()
	}
	go h.closeButtons(msg, "→ "+label)
	return label
}

func choicePrompt(question, label string) string {
	return fmt.Sprintf("[Pressed a button under your message %q]\n\n%s", shorten(question, 200), label)
}

func shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func (h *Handler) snooze(msg *models.Message, key chatqueue.Key, after time.Duration) string {
	if h.Tasks == nil || msg.Text == "" {
		return staleButton
	}
	at, err := h.Tasks.Snooze(key.ChatID, key.ThreadID, msg.Text, after)
	if err != nil {
		log.Printf("handler: snooze in chat=%d thread=%d: %v", key.ChatID, key.ThreadID, err)
		return "Не удалось отложить: " + err.Error()
	}
	note := "⏰ Отложено до " + when(at, time.Now())
	go h.closeButtons(msg, note)
	return note
}

func when(t, now time.Time) string {
	now = now.In(t.Location())
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return ru.Date(t)
}

func (h *Handler) closeButtons(msg *models.Message, note string) {
	h.edit(msg, msg.Text+"\n\n"+note, msg.Entities, nil)
}

func (h *Handler) edit(msg *models.Message, text string, entities []models.MessageEntity, kb buttons.Keyboard) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Buttons.Edit(ctx, msg, text, entities, kb); err != nil {
		log.Printf("handler: edit message %d in chat=%d: %v", msg.ID, msg.Chat.ID, err)
	}
}

func (h *Handler) answer(queryID, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Buttons.Answer(ctx, queryID, text); err != nil {
		log.Printf("handler: answer button press: %v", err)
	}
}
