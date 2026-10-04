package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"gorm.io/gorm"

	"github.com/nickalie/nclaw/internal/authwatch"
	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/cli/claude"
	"github.com/nickalie/nclaw/internal/cli/claudish"
	"github.com/nickalie/nclaw/internal/cli/codex"
	"github.com/nickalie/nclaw/internal/cli/copilot"
	"github.com/nickalie/nclaw/internal/cli/gemini"
	"github.com/nickalie/nclaw/internal/config"
	"github.com/nickalie/nclaw/internal/db"
	"github.com/nickalie/nclaw/internal/handler"
	"github.com/nickalie/nclaw/internal/invoker"
	"github.com/nickalie/nclaw/internal/pipeline"
	"github.com/nickalie/nclaw/internal/progress"
	"github.com/nickalie/nclaw/internal/scheduler"
	"github.com/nickalie/nclaw/internal/sendfile"
	"github.com/nickalie/nclaw/internal/skills"
	"github.com/nickalie/nclaw/internal/transcribe"
	"github.com/nickalie/nclaw/internal/version"
	"github.com/nickalie/nclaw/internal/webhook"
)

func main() {
	if hasFlag("-v", "--version") {
		if err := printVersion(); err != nil {
			os.Exit(1)
		}
		return
	}

	if err := config.Init(); err != nil {
		log.Fatal(err)
	}
	config.LogSecurityWarnings()

	database, err := db.Open(config.DBPath())
	if err != nil {
		log.Fatal("db open: ", err)
	}

	if err := db.Init(database); err != nil {
		log.Fatal("db init: ", err)
	}

	// Create CLI provider and verify it's available before starting.
	provider, err := newProvider(config.CLI())
	if err != nil {
		log.Fatal(err)
	}
	cliVer, err := provider.Version()
	if err != nil {
		log.Fatalf("%s cli not found: %v", provider.Name(), err)
	}

	installBundledSkills()
	a := setupBot(database, provider)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("nclaw bot started (%s, %s: %s)", version.String(), provider.Name(), cliVer)
	sendStartupNotifications(a.bot)
	a.startWatchers(ctx)
	a.bot.Start(ctx)
	stop()

	log.Println("nclaw: shutting down")
	a.shutdown(shutdownTimeout)
	log.Println("nclaw: stopped")
}

const shutdownTimeout = 20 * time.Second

type app struct {
	bot        *bot.Bot
	handler    *handler.Handler
	queue      *chatqueue.Queue[handler.Inbound]
	sched      *scheduler.Scheduler
	webhookMgr *webhook.Manager
	webhookSrv *webhook.Server
	authWatch  *authwatch.Watcher
}

func (a *app) startWatchers(ctx context.Context) {
	if a.authWatch != nil {
		go a.authWatch.Run(ctx)
	}
}

func (a *app) shutdown(timeout time.Duration) {
	if a.webhookSrv != nil {
		if err := a.webhookSrv.Shutdown(); err != nil {
			log.Printf("webhook shutdown: %v", err)
		}
	}

	a.handler.NotifyShutdown(a.queue.Close())

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := a.queue.Wait(ctx); err != nil {
		log.Printf("nclaw: running work did not finish: %v", err)
	}

	if err := a.sched.Shutdown(); err != nil {
		log.Printf("scheduler shutdown: %v", err)
	}
	if a.webhookMgr != nil {
		a.webhookMgr.Wait()
	}
}

func setupBot(database *gorm.DB, provider cli.Provider) *app {
	loc := config.Location()
	inv := invoker.New(provider, invokerOptions(loc, func(chatID int64, threadID int) string {
		return scheduler.FormatTaskList(database, loc, chatID, threadID)
	}))
	h := &handler.Handler{Invoker: inv}
	queue := chatqueue.New(h.RunBatch, chatqueue.Options{})
	h.Queue = queue

	b, err := bot.New(config.TelegramBotToken(),
		bot.WithNotAsyncHandlers(),
		bot.WithMiddlewares(handler.AllowChat),
		bot.WithDefaultHandler(h.Default),
		bot.WithHTTPClient(time.Minute, &http.Client{Timeout: 5 * time.Minute}),
	)
	if err != nil {
		log.Fatal(err)
	}
	h.Bot = b
	h.Send = newPipelineSendFunc(b)
	if config.Progress() {
		h.Progress = progress.NewBotAPI(b)
	}
	h.Transcriber = newTranscriber()
	if config.Reactions() {
		h.React = newReactFunc(b)
	}
	if config.LiveDrafts() {
		h.Drafts = draftAPI{b: b}
	}

	fileSenders := sendfile.Senders{
		Doc:   newSendDocFunc(b),
		Audio: newSendAudioFunc(b),
	}
	sched, err := scheduler.New(database, inv, queue, loc)
	if err != nil {
		log.Fatal("scheduler: ", err)
	}

	webhookMgr := createWebhookManager(database, inv, queue)
	p := buildPipeline(b, fileSenders, sched, webhookMgr)
	h.Pipeline = p
	sched.SetPipeline(p)
	if webhookMgr != nil {
		webhookMgr.SetPipeline(p)
	}
	authWatch := wireAuth(provider, h, p, loc)
	registerCommands(b, h)

	// Load tasks and start scheduler before webhook server to avoid a race where
	// an incoming webhook creates a task that LoadTasks then re-registers as a duplicate job.
	sched.LoadTasks()
	sched.Start()

	// Start webhook HTTP server after pipeline is wired and scheduler is loaded.
	webhookSrv := startWebhookServer(webhookMgr)

	return &app{bot: b, handler: h, queue: queue, sched: sched, webhookMgr: webhookMgr, webhookSrv: webhookSrv, authWatch: authWatch}
}

func wireAuth(provider cli.Provider, h *handler.Handler, p *pipeline.Pipeline, loc *time.Location) *authwatch.Watcher {
	auth, ok := provider.(cli.AuthProvider)
	if !ok {
		return nil
	}
	admin := config.AdminChatID()
	h.Logins = newLogins(provider, admin)
	renewHint, failureHint := signInHints(h.Logins != nil)
	p.SetFailureHint(func(output string) string {
		if auth.IsAuthFailure(output) {
			return failureHint
		}
		return ""
	})

	w := authwatch.New(authwatch.Options{
		Name:      "Claude",
		Expiry:    auth.AuthExpiry,
		RenewHint: renewHint,
		Location:  loc,
		Notify: func(ctx context.Context, text string) error {
			return h.Send(ctx, pipeline.Dest{ChatID: admin}, text, "")
		},
	})
	h.AuthStatus = w.Status
	if admin == 0 {
		log.Println("authwatch: no admin chat, sign-in expiry warnings are off (set NCLAW_ADMIN_CHAT_ID)")
		return nil
	}
	log.Printf("authwatch: sign-in expiry warnings go to chat %d", admin)
	return w
}

func newLogins(provider cli.Provider, admin int64) *handler.Logins {
	lp, ok := provider.(cli.LoginProvider)
	if !ok || admin <= 0 {
		return nil
	}
	return &handler.Logins{Provider: lp, AdminChatID: admin}
}

func signInHints(loginEnabled bool) (renew, failure string) {
	const expired = "🔑 The bot's Claude sign-in has expired or was revoked. "
	if loginEnabled {
		return "Send /login here to sign in again.", expired + "Send /login in the admin's private chat with the bot to sign in again."
	}
	const manual = "To sign in again, run claude auth login inside the bot's container."
	return manual, expired + manual
}

func newTranscriber() handler.Transcriber {
	t, err := transcribe.New(&transcribe.Config{
		WhisperBin: config.WhisperBin(),
		FFmpegBin:  "ffmpeg",
		Model:      config.WhisperModel(),
		Language:   config.WhisperLanguage(),
	})
	if err != nil {
		log.Printf("transcribe: voice transcription disabled: %v", err)
		return nil
	}
	log.Printf("transcribe: enabled (model=%s, language=%s)", config.WhisperModel(), config.WhisperLanguage())
	return t
}

func installBundledSkills() {
	claudeDir, err := claude.ConfigDir()
	if err != nil {
		log.Printf("skills: %v", err)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("skills: %v", err)
		return
	}

	state := filepath.Join(config.DataDir(), ".nclaw-skills.json")
	for _, dest := range skills.Dirs(config.CLI(), claudeDir, home) {
		rep, err := skills.Install(config.BundledSkillsDir(), dest, state)
		if err != nil {
			log.Printf("skills: %v", err)
		}
		logSkillReport(dest, &rep)
	}
}

func logSkillReport(dest string, rep *skills.Report) {
	if len(rep.Installed) > 0 {
		log.Printf("skills: installed %v into %s", rep.Installed, dest)
	}
	if len(rep.Updated) > 0 {
		log.Printf("skills: updated %v in %s to the bundled version", rep.Updated, dest)
	}
	if len(rep.Kept) > 0 {
		log.Printf("skills: kept %v in %s as is: they differ from the bundled version and from what nclaw installed", rep.Kept, dest)
	}
}

func registerCommands(b *bot.Bot, h *handler.Handler) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if me, err := b.GetMe(ctx); err != nil {
		log.Printf("telegram: getMe: %v", err)
	} else {
		h.BotUsername = me.Username
	}
	b.RegisterHandlerMatchFunc(h.MatchCommand, h.Command)
	b.RegisterHandlerMatchFunc(h.MatchStopGeneration, h.StopGeneration)
	if _, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: handler.Commands}); err != nil {
		log.Printf("telegram: setMyCommands: %v", err)
	}
	if h.Logins != nil {
		adminCommands := append(slices.Clone(handler.Commands), handler.LoginCommand)
		scope := &models.BotCommandScopeChat{ChatID: h.Logins.AdminChatID}
		if _, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{Commands: adminCommands, Scope: scope}); err != nil {
			log.Printf("telegram: setMyCommands for the admin chat: %v", err)
		}
	}
}

func invokerOptions(loc *time.Location, taskList invoker.TaskListFunc) invoker.Options {
	opts := invoker.Options{
		DataDir:         config.DataDir(),
		MaxSessionBytes: config.MaxSessionBytes(),
		Location:        loc,
		TaskList:        taskList,
		Timeout:         config.CLITimeout(),
	}
	if dir, err := claude.ConfigDir(); err == nil {
		opts.SkillsDir = filepath.Join(dir, "skills")
	}
	return opts
}

func buildPipeline(
	b *bot.Bot, fileSenders sendfile.Senders,
	sched *scheduler.Scheduler, webhookMgr *webhook.Manager,
) *pipeline.Pipeline {
	executors := []pipeline.BlockExecutor{sched}
	if webhookMgr != nil {
		executors = append(executors, webhookMgr)
	}
	fileSenders.MediaGroup = newSendMediaGroupFunc(b)
	p := pipeline.New(newPipelineSendFunc(b), fileSenders, webhookMgr != nil, executors...)
	p.SetStreamMessages(config.StreamMessages())
	p.SetDataDir(config.DataDir())
	return p
}

func hasFlag(flags ...string) bool {
	for _, arg := range os.Args[1:] {
		for _, f := range flags {
			if arg == f {
				return true
			}
		}
	}
	return false
}

func printVersion() error {
	fmt.Printf("nclaw %s\n", version.String())
	config.Init() //nolint:errcheck // best-effort: show CLI version if config is available
	provider, err := newProvider(config.CLI())
	if err != nil {
		fmt.Fprintf(os.Stderr, "cli: %v\n", err)
		return err
	}
	v, err := provider.Version()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: error: %v\n", provider.Name(), err)
		return err
	}
	fmt.Printf("%s: %s\n", provider.Name(), v)
	return nil
}

func newProvider(backend string) (cli.Provider, error) {
	switch backend {
	case "claude":
		p := claude.NewProvider(config.ClaudeExecPath())
		p.SetLoginEmail(config.LoginEmail())
		return p, nil
	case "claudish":
		return claudish.NewProvider(
			config.Model(), config.ModelOpus(), config.ModelSonnet(), config.ModelHaiku(), config.ModelSubagent(),
		), nil
	case "codex":
		return codex.NewProvider(), nil
	case "copilot":
		return copilot.NewProvider(config.CopilotModel()), nil
	case "gemini":
		return gemini.NewProvider(), nil
	default:
		return nil, fmt.Errorf("unsupported cli backend %q (valid: %v)", backend, config.ValidCLIBackends())
	}
}

func sendStartupNotifications(b *bot.Bot) {
	if !config.StartupNotification() {
		return
	}

	chatIDs := config.WhitelistChatIDs()
	if len(chatIDs) == 0 {
		return
	}

	text := "nclaw bot started\n" + version.String()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	for _, chatID := range chatIDs {
		if _, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: chatID,
			Text:   text,
		}); err != nil {
			log.Printf("startup notify chat %d: %v", chatID, err)
		}
	}
}

func newSendDocFunc(b *bot.Bot) sendfile.SendDocFunc {
	return func(ctx context.Context, chatID int64, threadID int, filename string, data []byte, caption string) error {
		_, err := b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID:          chatID,
			MessageThreadID: threadID,
			Document:        &models.InputFileUpload{Filename: filename, Data: bytes.NewReader(data)},
			Caption:         caption,
		})
		return err
	}
}

func newSendAudioFunc(b *bot.Bot) sendfile.SendAudioFunc {
	return func(ctx context.Context, chatID int64, threadID int, filename string, data []byte, caption string) error {
		_, err := b.SendAudio(ctx, &bot.SendAudioParams{
			ChatID:          chatID,
			MessageThreadID: threadID,
			Audio:           &models.InputFileUpload{Filename: filename, Data: bytes.NewReader(data)},
			Caption:         caption,
		})
		return err
	}
}

func newSendMediaGroupFunc(b *bot.Bot) sendfile.SendMediaGroupFunc {
	return func(ctx context.Context, chatID int64, threadID int, files []sendfile.File) error {
		media := make([]models.InputMedia, len(files))
		for i, f := range files {
			media[i] = buildInputMedia(f)
		}
		_, err := b.SendMediaGroup(ctx, &bot.SendMediaGroupParams{
			ChatID:          chatID,
			MessageThreadID: threadID,
			Media:           media,
		})
		return err
	}
}

func buildInputMedia(f sendfile.File) models.InputMedia {
	attach := "attach://" + f.Filename
	reader := bytes.NewReader(f.Data)
	switch f.MediaType {
	case sendfile.MediaAudio:
		return &models.InputMediaAudio{
			Media: attach, Caption: f.Caption, MediaAttachment: reader,
		}
	case sendfile.MediaPhoto:
		return &models.InputMediaPhoto{
			Media: attach, Caption: f.Caption, MediaAttachment: reader,
		}
	case sendfile.MediaVideo:
		return &models.InputMediaVideo{
			Media: attach, Caption: f.Caption, MediaAttachment: reader,
		}
	default:
		return &models.InputMediaDocument{
			Media: attach, Caption: f.Caption, MediaAttachment: reader,
		}
	}
}

type draftAPI struct {
	b *bot.Bot
}

func (a draftAPI) SendDraft(ctx context.Context, chatID int64, threadID int, draftID int64, text string) error {
	_, err := a.b.SendMessageDraft(ctx, &bot.SendMessageDraftParams{
		ChatID:          chatID,
		MessageThreadID: threadID,
		DraftID:         strconv.FormatInt(draftID, 10),
		Text:            text,
		CanStop:         true,
	})
	return err
}

func newReactFunc(b *bot.Bot) handler.MessageReactor {
	return func(ctx context.Context, chatID int64, msgID int, emoji string) error {
		params := &bot.SetMessageReactionParams{ChatID: chatID, MessageID: msgID, Reaction: []models.ReactionType{}}
		if emoji != "" {
			params.Reaction = []models.ReactionType{{
				Type:              models.ReactionTypeTypeEmoji,
				ReactionTypeEmoji: &models.ReactionTypeEmoji{Emoji: emoji},
			}}
		}
		_, err := b.SetMessageReaction(ctx, params)
		return err
	}
}

func newPipelineSendFunc(b *bot.Bot) pipeline.SendFunc {
	return func(ctx context.Context, dest pipeline.Dest, text, parseMode string) error {
		params := &bot.SendMessageParams{
			ChatID:          dest.ChatID,
			MessageThreadID: dest.ThreadID,
			Text:            text,
		}
		if dest.ReplyTo != 0 {
			params.ReplyParameters = &models.ReplyParameters{MessageID: dest.ReplyTo, AllowSendingWithoutReply: true}
		}
		if parseMode != "" {
			params.ParseMode = models.ParseMode(parseMode)
		}
		_, err := b.SendMessage(ctx, params)
		return err
	}
}

func createWebhookManager(database *gorm.DB, inv *invoker.Invoker, runner webhook.Runner) *webhook.Manager {
	domain := config.WebhookBaseDomain()
	if domain == "" {
		return nil
	}
	return webhook.NewManager(database, inv, runner, domain)
}

func startWebhookServer(mgr *webhook.Manager) *webhook.Server {
	if mgr == nil {
		return nil
	}

	srv := webhook.NewServer(mgr)

	listenErr := make(chan error, 1)
	go func() {
		if err := srv.Listen(config.WebhookPort()); err != nil {
			listenErr <- err
		}
	}()

	// Give the listener a moment to fail on bind errors.
	select {
	case err := <-listenErr:
		log.Fatalf("webhook server failed to start: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	// Monitor for runtime listener failures.
	go func() {
		if err, ok := <-listenErr; ok {
			log.Fatalf("webhook server error: %v", err)
		}
	}()

	return srv
}
