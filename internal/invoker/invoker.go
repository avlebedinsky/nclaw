// Package invoker runs a CLI backend for a chat: it picks the working directory,
// builds the shared system prompt and invokes the client.
package invoker

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/telegram"
)

// Mode selects how a run relates to the chat's persistent session.
type Mode int

// Run modes.
const (
	Continue Mode = iota
	Isolated
)

const freshSessionMarker = ".nclaw-new-session"

// IsolatedDirName is the chat subdirectory where isolated runs execute, so their
// sessions never become the chat's most recent conversation.
const IsolatedDirName = ".isolated"

// TaskListFunc returns the scheduled-task section of the system prompt for a chat/thread.
type TaskListFunc func(chatID int64, threadID int) string

// Options configures an Invoker.
type Options struct {
	DataDir         string
	SkillsDir       string
	MaxSessionBytes int64
	Location        *time.Location
	Now             func() time.Time
	TaskList        TaskListFunc
	Timeout         time.Duration
}

// TimeoutError is the cause of a run stopped for exceeding Options.Timeout.
type TimeoutError struct {
	After time.Duration
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("timed out after %s", e.After)
}

// Request describes one CLI run.
type Request struct {
	ChatID    int64
	ThreadID  int
	Prompt    string
	Mode      Mode
	Configure func(cli.Client)
}

// Outcome is the result of a run. Result is never nil.
type Outcome struct {
	Result  *cli.Result
	Err     error
	Dir     string
	ChatDir string
}

// Invoker runs the configured CLI provider on behalf of every input channel.
type Invoker struct {
	provider cli.Provider
	opts     Options
}

// New creates an Invoker. Missing Location and Now default to time.Local and time.Now.
func New(provider cli.Provider, opts Options) *Invoker {
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Invoker{provider: provider, opts: opts}
}

// ProviderName returns the name of the underlying CLI backend.
func (i *Invoker) ProviderName() string {
	return i.provider.Name()
}

// ChatDir returns the working directory of a chat/thread.
func (i *Invoker) ChatDir(chatID int64, threadID int) string {
	return telegram.ChatDir(i.opts.DataDir, chatID, threadID)
}

// ResetSession makes the chat's next run start a new conversation: providers that store
// sessions archive the current one, others start fresh on the next run via a marker file.
func (i *Invoker) ResetSession(chatID int64, threadID int) error {
	dir := i.ChatDir(chatID, threadID)
	if store, ok := i.provider.(cli.SessionStore); ok {
		return store.ArchiveSession(dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, freshSessionMarker), nil, 0o644)
}

// SessionSize reports the size of the chat's current session, when the provider can measure it.
func (i *Invoker) SessionSize(chatID int64, threadID int) (int64, bool) {
	store, ok := i.provider.(cli.SessionStore)
	if !ok {
		return 0, false
	}
	return store.SessionSize(i.ChatDir(chatID, threadID))
}

// MaxSessionBytes returns the session size that triggers an automatic reset; 0 means never.
func (i *Invoker) MaxSessionBytes() int64 {
	return i.opts.MaxSessionBytes
}

// Run executes one CLI invocation for the request. Callers serialize runs per chat.
func (i *Invoker) Run(ctx context.Context, req Request) Outcome {
	out := i.dirs(req)
	if err := os.MkdirAll(out.Dir, 0o755); err != nil {
		out.Result, out.Err = &cli.Result{}, fmt.Errorf("invoker: mkdir %s: %w", out.Dir, err)
		return out
	}

	if err := i.provider.PreInvoke(); err != nil {
		log.Printf("invoker: pre-invoke warning: %v", err)
	}
	if req.Mode == Continue {
		i.maybeResetSession(out.Dir)
	}

	runCtx, cancel := i.withTimeout(ctx)
	defer cancel()

	client := i.provider.NewClient().Context(runCtx).Dir(out.Dir).SkipPermissions().
		AppendSystemPrompt(i.systemPrompt(req.ChatID, req.ThreadID))
	if req.Configure != nil {
		req.Configure(client)
	}

	log.Printf("invoker: running %s in dir=%s", i.provider.Name(), out.Dir)
	out.Result, out.Err = i.execute(client, req, out)
	if out.Result == nil {
		out.Result = &cli.Result{}
	}
	if out.Err != nil {
		log.Printf("invoker: %s error in dir=%s: %v", i.provider.Name(), out.Dir, out.Err)
	}
	return out
}

func (i *Invoker) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if i.opts.Timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeoutCause(ctx, i.opts.Timeout, &TimeoutError{After: i.opts.Timeout})
}

func (i *Invoker) dirs(req Request) Outcome {
	chatDir := i.ChatDir(req.ChatID, req.ThreadID)
	if req.Mode == Isolated {
		return Outcome{Dir: filepath.Join(chatDir, IsolatedDirName), ChatDir: chatDir}
	}
	return Outcome{Dir: chatDir, ChatDir: chatDir}
}

func (i *Invoker) execute(client cli.Client, req Request, out Outcome) (*cli.Result, error) {
	if req.Mode != Isolated {
		return i.continueOrStart(client, out.Dir, req.Prompt)
	}
	if ec, ok := client.(cli.EphemeralClient); ok {
		client = ec.Ephemeral()
	}
	note := fmt.Sprintf("[Isolated run in %s; the chat's files are in %s]\n\n", out.Dir, out.ChatDir)
	return client.Ask(i.timeHeader() + note + req.Prompt)
}

func (i *Invoker) continueOrStart(client cli.Client, dir, prompt string) (*cli.Result, error) {
	marker := filepath.Join(dir, freshSessionMarker)
	if _, err := os.Stat(marker); err != nil {
		return client.Continue(i.timeHeader() + prompt)
	}

	result, err := client.Ask(i.timeHeader() + prompt)
	if err == nil {
		if rmErr := os.Remove(marker); rmErr != nil {
			log.Printf("invoker: remove %s: %v", marker, rmErr)
		}
	}
	return result, err
}

func (i *Invoker) systemPrompt(chatID int64, threadID int) string {
	parts := []string{telegram.Prompt, i.chatContext(chatID, threadID), i.timezoneLine()}
	if i.opts.TaskList != nil {
		parts = append(parts, i.opts.TaskList(chatID, threadID))
	}
	if skills := i.skillsPrompt(); skills != "" {
		parts = append(parts, skills)
	}
	return strings.Join(parts, "\n\n")
}

func (i *Invoker) chatContext(chatID int64, threadID int) string {
	chatDir := i.ChatDir(chatID, 0)
	lines := []string{chatLine(chatID, telegram.ChatName(chatDir))}
	if topic := telegram.TopicName(i.ChatDir(chatID, threadID)); threadID != 0 && topic != "" {
		lines = append(lines, "This conversation is the topic «"+topic+"» of that chat; every topic is a separate conversation.")
	}
	if rule := i.sharedMemoryRule(chatID, threadID, chatDir); rule != "" {
		lines = append(lines, rule)
	}
	return strings.Join(lines, "\n")
}

func chatLine(chatID int64, name string) string {
	private := telegram.IsPrivateChat(chatID)
	switch {
	case private && name != "":
		return "This is a private Telegram chat with " + name + "."
	case private:
		return "This is a private Telegram chat."
	case name != "":
		return "This is the Telegram group «" + name + "». Messages from people start with [From: name]."
	default:
		return "This is a Telegram group chat. Messages from people start with [From: name]."
	}
}

func (i *Invoker) sharedMemoryRule(chatID int64, threadID int, chatDir string) string {
	mp, ok := i.provider.(cli.MemoryFileProvider)
	singleConversation := telegram.IsPrivateChat(chatID) && threadID == 0
	if !ok || singleConversation {
		return ""
	}
	return "Your auto memory belongs to this conversation only. Facts that every conversation in this chat should know " +
		"(who the people are, what each topic is for, shared conventions) go into " + filepath.Join(chatDir, mp.MemoryFile()) +
		", which all of them load: keep it short, one fact per line, and remove what is no longer true. " +
		"Never save passwords, tokens or card numbers in any memory."
}

func (i *Invoker) timezoneLine() string {
	offset := i.opts.Now().In(i.opts.Location).Format("-07:00")
	return fmt.Sprintf("Scheduler timezone: %s (UTC%s). Cron expressions and \"once\" timestamps are interpreted in this timezone.",
		i.opts.Location, offset)
}

func (i *Invoker) timeHeader() string {
	now := i.opts.Now().In(i.opts.Location)
	return "[Current time: " + now.Format("Monday, 2006-01-02 15:04:05 MST (-07:00)") + "]\n\n"
}

func (i *Invoker) skillsPrompt() string {
	if n, ok := i.provider.(cli.NativeSkillsProvider); ok && n.NativeSkills() {
		return ""
	}
	if i.opts.SkillsDir == "" {
		return ""
	}
	return loadSkillsPrompt(i.opts.SkillsDir)
}

func (i *Invoker) maybeResetSession(dir string) {
	store, ok := i.provider.(cli.SessionStore)
	if !ok || i.opts.MaxSessionBytes <= 0 {
		return
	}

	size, found := store.SessionSize(dir)
	if !found || size < i.opts.MaxSessionBytes {
		return
	}

	if err := store.ArchiveSession(dir); err != nil {
		log.Printf("invoker: failed to reset oversized session in dir=%s: %v", dir, err)
		return
	}
	log.Printf("invoker: reset session in dir=%s (was %d bytes, threshold %d bytes)", dir, size, i.opts.MaxSessionBytes)
}
