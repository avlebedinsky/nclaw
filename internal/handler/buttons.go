package handler

import (
	"context"
	"log"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/nickalie/nclaw/internal/buttons"
	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/model"
)

const staleButton = "This button no longer works."

// ButtonAPI answers presses on inline buttons and edits the messages that carry them.
type ButtonAPI interface {
	Answer(ctx context.Context, queryID, text string) error
	Edit(ctx context.Context, msg *models.Message, text string, kb buttons.Keyboard) error
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
		toast = h.press(msg, buttons.Parse(q.Data))
	}
	go h.answer(q.ID, toast)
}

func (h *Handler) press(msg *models.Message, p buttons.Press) string {
	key := chatqueue.Key{ChatID: msg.Chat.ID, ThreadID: msg.MessageThreadID}
	switch p.Kind {
	case buttons.Done:
		go h.closeButtons(msg, "✅ Done")
		return "Done"
	case buttons.Snooze:
		return h.snooze(msg, key, p.After)
	default:
		return staleButton
	}
}

func (h *Handler) snooze(msg *models.Message, key chatqueue.Key, after time.Duration) string {
	if h.Tasks == nil || msg.Text == "" {
		return staleButton
	}
	at, err := h.Tasks.Snooze(key.ChatID, key.ThreadID, msg.Text, after)
	if err != nil {
		log.Printf("handler: snooze in chat=%d thread=%d: %v", key.ChatID, key.ThreadID, err)
		return "Could not snooze: " + err.Error()
	}
	note := "⏰ Snoozed until " + when(at, time.Now())
	go h.closeButtons(msg, note)
	return note
}

func when(t, now time.Time) string {
	now = now.In(t.Location())
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return t.Format("15:04")
	}
	return t.Format("Mon 2 Jan 15:04")
}

func (h *Handler) closeButtons(msg *models.Message, note string) {
	h.edit(msg, msg.Text+"\n\n"+note, nil)
}

func (h *Handler) edit(msg *models.Message, text string, kb buttons.Keyboard) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Buttons.Edit(ctx, msg, text, kb); err != nil {
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
