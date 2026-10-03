package handler

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/pipeline"
)

var commandRe = regexp.MustCompile(`^/([a-zA-Z0-9_]{1,32})(?:@([A-Za-z0-9_]+))?(?:\s|$)`)

// Commands lists the bot commands handled by nclaw itself, for SetMyCommands.
var Commands = []models.BotCommand{
	{Command: "stop", Description: "Stop the current run and drop queued messages"},
	{Command: "new", Description: "Start a new conversation"},
	{Command: "status", Description: "Show what the bot is doing in this chat"},
}

func parseCommand(text, botUsername string) (name string, forMe, ok bool) {
	m := commandRe.FindStringSubmatch(text)
	if m == nil {
		return "", false, false
	}
	return strings.ToLower(m[1]), m[2] == "" || strings.EqualFold(m[2], botUsername), true
}

// MatchCommand reports whether the update is one of nclaw's own commands addressed to this bot.
func (h *Handler) MatchCommand(update *models.Update) bool {
	if update.Message == nil {
		return false
	}
	name, forMe, ok := parseCommand(update.Message.Text, h.BotUsername)
	return ok && forMe && h.commandFunc(name) != nil
}

// Command runs one of nclaw's own commands without waiting for the chat's queue.
func (h *Handler) Command(_ context.Context, _ *bot.Bot, update *models.Update) {
	msg := update.Message
	name, _, _ := parseCommand(msg.Text, h.BotUsername)
	key := chatqueue.Key{ChatID: msg.Chat.ID, ThreadID: msg.MessageThreadID}
	log.Printf("handler: command /%s from chat=%d thread=%d", name, key.ChatID, key.ThreadID)
	h.commandFunc(name)(key)
}

func (h *Handler) commandFunc(name string) func(chatqueue.Key) {
	switch name {
	case "stop":
		return h.stop
	case "new":
		return h.newSession
	case "status":
		return h.status
	default:
		return nil
	}
}

func (h *Handler) stop(key chatqueue.Key) {
	res := h.Queue.Stop(key)
	go h.notify(key, stopText(res))
}

func stopText(res chatqueue.StopResult) string {
	if !res.Canceled && res.DroppedUser == 0 && res.DroppedJobs == 0 {
		return "Nothing to stop."
	}
	var parts []string
	if res.Canceled {
		parts = append(parts, fmt.Sprintf("⏹ Stopped %s after %s.", kindName(res.Kind), res.Elapsed.Round(time.Second)))
	}
	if res.DroppedUser > 0 {
		parts = append(parts, fmt.Sprintf("Dropped %d queued message(s).", res.DroppedUser))
	}
	if res.DroppedJobs > 0 {
		parts = append(parts, fmt.Sprintf("Dropped %d queued task/webhook run(s).", res.DroppedJobs))
	}
	return strings.Join(parts, " ")
}

func (h *Handler) newSession(key chatqueue.Key) {
	job := chatqueue.Job{Kind: chatqueue.KindControl, Label: "new session", Fn: func(context.Context) {
		text := "🆕 New conversation started."
		if err := h.Invoker.ResetSession(key.ChatID, key.ThreadID); err != nil {
			log.Printf("handler: reset session chat=%d thread=%d: %v", key.ChatID, key.ThreadID, err)
			text = "Could not start a new conversation: " + err.Error()
		}
		h.notify(key, text)
	}}
	if _, err := h.Queue.Submit(key, job); err != nil {
		go h.notify(key, "Could not start a new conversation: "+err.Error())
	}
}

func (h *Handler) status(key chatqueue.Key) {
	snap := h.Queue.Snapshot(key)
	size, hasSize := h.Invoker.SessionSize(key.ChatID, key.ThreadID)
	go h.notify(key, statusText(&snap, size, hasSize, h.Invoker.MaxSessionBytes(), h.Invoker.ProviderName()))
}

func statusText(snap *chatqueue.Snapshot, size int64, hasSize bool, maxBytes int64, backend string) string {
	lines := []string{"💤 Idle."}
	if snap.Running {
		lines[0] = fmt.Sprintf("▶️ Running %s for %s.", kindName(snap.Kind), time.Since(snap.Started).Round(time.Second))
		if snap.Kind == chatqueue.KindUser && snap.Batch > 1 {
			lines[0] += fmt.Sprintf(" (%d messages merged)", snap.Batch)
		}
	}
	if snap.PendingUser > 0 || snap.PendingJobs > 0 {
		lines = append(lines, fmt.Sprintf("Queued: %d message(s), %d task/webhook run(s).", snap.PendingUser, snap.PendingJobs))
	}
	if hasSize {
		lines = append(lines, sessionLine(size, maxBytes))
	}
	return strings.Join(append(lines, "Backend: "+backend+"."), "\n")
}

func sessionLine(size, maxBytes int64) string {
	line := "Session: " + humanBytes(size)
	if maxBytes > 0 {
		line += fmt.Sprintf(" of %s (%d%%)", humanBytes(maxBytes), size*100/maxBytes)
	}
	return line + "."
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func kindName(k chatqueue.Kind) string {
	switch k {
	case chatqueue.KindScheduled:
		return "a scheduled task"
	case chatqueue.KindWebhook:
		return "a webhook request"
	case chatqueue.KindControl:
		return "a control command"
	default:
		return "your request"
	}
}

// NotifyShutdown tells every chat whose request was cut short by a shutdown that it
// should send it again.
func (h *Handler) NotifyShutdown(report []chatqueue.Interrupted) {
	var wg sync.WaitGroup
	for _, item := range report {
		if text := shutdownText(item); text != "" {
			wg.Go(func() { h.notifyWithin(item.Key, text, 3*time.Second) })
		}
	}
	wg.Wait()
}

func shutdownText(item chatqueue.Interrupted) string {
	var parts []string
	switch {
	case item.Running && item.Kind == chatqueue.KindUser:
		parts = append(parts, "♻️ The bot is restarting and your request was interrupted. "+
			"Please send it again in a minute — the conversation is kept.")
	case item.Running:
		parts = append(parts, "♻️ The bot is restarting; "+kindName(item.Kind)+" in progress was interrupted.")
	}
	if item.DroppedUser > 0 {
		if len(parts) == 0 {
			parts = append(parts, "♻️ The bot is restarting.")
		}
		parts = append(parts, fmt.Sprintf("%d queued message(s) were not processed.", item.DroppedUser))
	}
	return strings.Join(parts, " ")
}

func (h *Handler) notify(key chatqueue.Key, text string) {
	h.notifyWithin(key, text, 15*time.Second)
}

func (h *Handler) notifyWithin(key chatqueue.Key, text string, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := h.Send(ctx, pipeline.Dest{ChatID: key.ChatID, ThreadID: key.ThreadID}, text, ""); err != nil {
		log.Printf("handler: notify chat=%d thread=%d: %v", key.ChatID, key.ThreadID, err)
	}
}
