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

	"github.com/nickalie/nclaw/internal/buttons"
	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/model"
	"github.com/nickalie/nclaw/internal/pipeline"
	"github.com/nickalie/nclaw/internal/ru"
	"github.com/nickalie/nclaw/internal/skills"
)

var commandRe = regexp.MustCompile(`^/([a-zA-Z0-9_]{1,32})(?:@([A-Za-z0-9_]+))?(?:\s|$)`)

// Commands lists the bot commands handled by nclaw itself, for SetMyCommands.
var Commands = []models.BotCommand{
	{Command: "stop", Description: "Остановить ответ и очистить очередь"},
	{Command: "new", Description: "Начать новый разговор"},
	{Command: "status", Description: "Что бот сейчас делает в этом чате"},
	{Command: "tasks", Description: "Задачи по расписанию в этом чате"},
	{Command: "model", Description: "Выбрать модель для этого чата"},
	{Command: "skills", Description: "Какие скиллы доступны здесь"},
}

// LoginCommand is offered only in the admin chat, where /login works.
var LoginCommand = models.BotCommand{Command: "login", Description: "Заново войти в Claude"}

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

// MatchStopGeneration reports whether the user pressed the stop button of a live draft.
func (h *Handler) MatchStopGeneration(update *models.Update) bool {
	return update.StoppedMessageGeneration != nil
}

// StopGeneration handles the stop button of a live draft like /stop.
func (h *Handler) StopGeneration(_ context.Context, _ *bot.Bot, update *models.Update) {
	stopped := update.StoppedMessageGeneration
	key := chatqueue.Key{ChatID: stopped.Chat.ID, ThreadID: stopped.MessageThreadID}
	log.Printf("handler: draft %d stopped in chat=%d thread=%d", stopped.DraftID, key.ChatID, key.ThreadID)
	h.stop(key)
}

func (h *Handler) commandFunc(name string) func(chatqueue.Key) {
	commands := map[string]func(chatqueue.Key){"stop": h.stop, "new": h.newSession, "status": h.status}
	if h.Tasks != nil {
		commands["tasks"] = h.tasks
	}
	if len(h.Invoker.Models()) > 0 {
		commands["model"] = h.model
	}
	if h.SkillDirs != nil {
		commands["skills"] = h.listSkills
	}
	if h.Logins != nil {
		commands["login"] = h.login
	}
	return commands[name]
}

func (h *Handler) stop(key chatqueue.Key) {
	res := h.Queue.Stop(key)
	go h.notify(key, stopText(res))
}

func stopText(res chatqueue.StopResult) string {
	if !res.Canceled && res.DroppedUser == 0 && res.DroppedJobs == 0 {
		return "Останавливать нечего."
	}
	var parts []string
	if res.Canceled {
		parts = append(parts, fmt.Sprintf("⏹ Остановлено через %s: %s.", ru.Duration(res.Elapsed), kindName(res.Kind)))
	}
	if res.DroppedUser > 0 {
		parts = append(parts, fmt.Sprintf("Убрано из очереди сообщений: %d.", res.DroppedUser))
	}
	if res.DroppedJobs > 0 {
		parts = append(parts, fmt.Sprintf("Убрано из очереди задач и вебхуков: %d.", res.DroppedJobs))
	}
	return strings.Join(parts, " ")
}

func (h *Handler) newSession(key chatqueue.Key) {
	job := chatqueue.Job{Kind: chatqueue.KindControl, Label: "new session", Fn: func(context.Context) {
		h.cutOff.Delete(key)
		text := "🆕 Начат новый разговор."
		if err := h.Invoker.ResetSession(key.ChatID, key.ThreadID); err != nil {
			log.Printf("handler: reset session chat=%d thread=%d: %v", key.ChatID, key.ThreadID, err)
			text = "Не удалось начать новый разговор: " + err.Error()
		}
		h.notify(key, text)
	}}
	if _, err := h.Queue.Submit(key, job); err != nil {
		go h.notify(key, "Не удалось начать новый разговор: "+err.Error())
	}
}

func (h *Handler) status(key chatqueue.Key) {
	snap := h.Queue.Snapshot(key)
	size, hasSize := h.Invoker.SessionSize(key.ChatID, key.ThreadID)
	text := statusText(&snap, size, hasSize, h.Invoker.MaxSessionBytes(), h.Invoker.ProviderName())
	if h.AuthStatus != nil {
		if line := h.AuthStatus(); line != "" {
			text += "\n" + line
		}
	}
	go h.notify(key, text)
}

func statusText(snap *chatqueue.Snapshot, size int64, hasSize bool, maxBytes int64, backend string) string {
	lines := []string{"💤 Сейчас ничего не выполняется."}
	if snap.Running {
		lines[0] = fmt.Sprintf("▶️ Выполняется %s, уже %s.", kindName(snap.Kind), ru.Duration(time.Since(snap.Started)))
		if snap.Kind == chatqueue.KindUser && snap.Batch > 1 {
			lines[0] += fmt.Sprintf(" Сообщений в одном запросе: %d.", snap.Batch)
		}
	}
	if snap.PendingUser > 0 || snap.PendingJobs > 0 {
		lines = append(lines, fmt.Sprintf("В очереди: сообщений — %d, задач и вебхуков — %d.", snap.PendingUser, snap.PendingJobs))
	}
	if hasSize {
		lines = append(lines, sessionLine(size, maxBytes))
	}
	return strings.Join(append(lines, "Бэкенд: "+backend+"."), "\n")
}

func sessionLine(size, maxBytes int64) string {
	line := "Сессия: " + humanBytes(size)
	if maxBytes > 0 {
		line += fmt.Sprintf(" из %s (%d%%)", humanBytes(maxBytes), size*100/maxBytes)
	}
	return line + "."
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f МБ", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f КБ", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d Б", n)
	}
}

func kindName(k chatqueue.Kind) string {
	switch k {
	case chatqueue.KindScheduled:
		return "задача по расписанию"
	case chatqueue.KindWebhook:
		return "запрос вебхука"
	case chatqueue.KindControl:
		return "служебная команда"
	default:
		return "ваш запрос"
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
		parts = append(parts, "♻️ Бот перезапускается, ваш запрос прерван. "+
			"Отправьте его ещё раз через минуту — разговор сохранится.")
	case item.Running:
		parts = append(parts, "♻️ Бот перезапускается, прервано: "+kindName(item.Kind)+".")
	}
	if item.DroppedUser > 0 {
		if len(parts) == 0 {
			parts = append(parts, "♻️ Бот перезапускается.")
		}
		parts = append(parts, fmt.Sprintf("Не обработаны сообщения из очереди: %d.", item.DroppedUser))
	}
	return strings.Join(parts, " ")
}

func (h *Handler) notify(key chatqueue.Key, text string) {
	h.notifyWithin(key, text, 15*time.Second)
}

func (h *Handler) notifyWithin(key chatqueue.Key, text string, timeout time.Duration) {
	h.sendWithin(key, text, nil, timeout)
}

func (h *Handler) sendWithin(key chatqueue.Key, text string, kb buttons.Keyboard, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	dest := pipeline.Dest{ChatID: key.ChatID, ThreadID: key.ThreadID, Buttons: kb}
	if err := h.Send(ctx, dest, text, ""); err != nil {
		log.Printf("handler: notify chat=%d thread=%d: %v", key.ChatID, key.ThreadID, err)
	}
}

func (h *Handler) model(key chatqueue.Key) {
	go func() {
		text, kb := h.modelView(key)
		h.sendWithin(key, text, kb, 15*time.Second)
	}()
}

func (h *Handler) modelView(key chatqueue.Key) (string, buttons.Keyboard) {
	current := h.Invoker.Model(key.ChatID, key.ThreadID)
	text := "Модель в этом чате: " + buttons.ModelName(current) + "."
	return text, buttons.Models(h.Invoker.Models(), current)
}

func (h *Handler) listSkills(key chatqueue.Key) {
	go h.notify(key, h.skillsText(key))
}

func (h *Handler) skillsText(key chatqueue.Key) string {
	local, global := h.SkillDirs(h.Invoker.ChatDir(key.ChatID, key.ThreadID))
	where := "этого чата"
	if key.ThreadID != 0 {
		where = "этого топика"
	}
	var sections []string
	if own := skills.List(local); len(own) > 0 {
		sections = append(sections, skillSection("🧩 Скиллы "+where+":", own))
	}
	if common := globalSkills(global); len(common) > 0 {
		sections = append(sections, skillSection(fmt.Sprintf("🧩 Общие скиллы (%d):", len(common)), common))
	}
	if len(sections) == 0 {
		return "Скиллов нет."
	}
	return strings.Join(sections, "\n\n")
}

func globalSkills(dirs []string) []skills.Info {
	seen := map[string]bool{}
	var list []skills.Info
	for _, dir := range dirs {
		for _, s := range skills.List(dir) {
			if !seen[s.Name] {
				seen[s.Name] = true
				list = append(list, s)
			}
		}
	}
	return list
}

func skillSection(title string, list []skills.Info) string {
	lines := make([]string, 0, len(list)+1)
	lines = append(lines, title)
	for _, s := range list {
		line := "• " + s.Name
		if d := firstSentence(s.Description); d != "" {
			line += " — " + shorten(d, 80)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}

func (h *Handler) tasks(key chatqueue.Key) {
	go func() {
		text, kb := h.taskList(key)
		h.sendWithin(key, text, kb, 15*time.Second)
	}()
}

func (h *Handler) taskList(key chatqueue.Key) (string, buttons.Keyboard) {
	tasks, err := h.Tasks.ChatTasks(key.ChatID, key.ThreadID)
	if err != nil {
		return "Не удалось получить задачи: " + err.Error(), nil
	}
	if len(tasks) == 0 {
		return "Здесь нет задач по расписанию.", nil
	}
	lines := []string{"Задачи по расписанию:"}
	kb := make(buttons.Keyboard, 0, len(tasks))
	for i := range tasks {
		t := &tasks[i]
		prompt := strings.Join(strings.Fields(t.Prompt), " ")
		lines = append(lines, fmt.Sprintf("%d. %s — %s", i+1, shorten(prompt, 60), scheduleText(t)))
		kb = append(kb, buttons.Task(i+1, t.ID, t.Status == model.StatusPaused))
	}
	return strings.Join(lines, "\n"), kb
}

func scheduleText(t *model.ScheduledTask) string {
	var next string
	if t.NextRun != nil {
		next = ru.Date(*t.NextRun)
	}
	var s string
	switch t.ScheduleType {
	case model.ScheduleOnce:
		s = "один раз, " + next
		next = ""
	case model.ScheduleCron:
		s = "cron " + t.ScheduleValue
	default:
		s = "раз в " + intervalText(t.ScheduleValue)
	}
	if t.Status == model.StatusPaused {
		return s + ", на паузе"
	}
	if next != "" {
		s += ", следующий запуск " + next
	}
	return s
}

func intervalText(value string) string {
	d, err := time.ParseDuration(value)
	if err != nil {
		return value
	}
	return ru.Duration(d)
}
