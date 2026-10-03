// Package invoker runs a CLI backend for a chat: it picks the working directory,
// serializes runs per chat, builds the shared system prompt and invokes the client.
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
	locker   *telegram.ChatLocker
	opts     Options
}

// New creates an Invoker. Missing Location and Now default to time.Local and time.Now.
func New(provider cli.Provider, locker *telegram.ChatLocker, opts Options) *Invoker {
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Invoker{provider: provider, locker: locker, opts: opts}
}

// ProviderName returns the name of the underlying CLI backend.
func (i *Invoker) ProviderName() string {
	return i.provider.Name()
}

// ChatDir returns the working directory of a chat/thread.
func (i *Invoker) ChatDir(chatID int64, threadID int) string {
	return telegram.ChatDir(i.opts.DataDir, chatID, threadID)
}

// Run executes one CLI invocation for the request, holding the chat's lock for its duration.
func (i *Invoker) Run(_ context.Context, req Request) Outcome {
	out := i.dirs(req)
	if err := os.MkdirAll(out.Dir, 0o755); err != nil {
		out.Result, out.Err = &cli.Result{}, fmt.Errorf("invoker: mkdir %s: %w", out.Dir, err)
		return out
	}

	unlock := i.locker.Lock(req.ChatID, req.ThreadID)
	defer unlock()

	if err := i.provider.PreInvoke(); err != nil {
		log.Printf("invoker: pre-invoke warning: %v", err)
	}
	if req.Mode == Continue {
		i.maybeResetSession(out.Dir)
	}

	client := i.provider.NewClient().Dir(out.Dir).SkipPermissions().
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

func (i *Invoker) dirs(req Request) Outcome {
	chatDir := i.ChatDir(req.ChatID, req.ThreadID)
	if req.Mode == Isolated {
		return Outcome{Dir: filepath.Join(chatDir, IsolatedDirName), ChatDir: chatDir}
	}
	return Outcome{Dir: chatDir, ChatDir: chatDir}
}

func (i *Invoker) execute(client cli.Client, req Request, out Outcome) (*cli.Result, error) {
	if req.Mode != Isolated {
		return client.Continue(i.timeHeader() + req.Prompt)
	}
	if ec, ok := client.(cli.EphemeralClient); ok {
		client = ec.Ephemeral()
	}
	note := fmt.Sprintf("[Isolated run in %s; the chat's files are in %s]\n\n", out.Dir, out.ChatDir)
	return client.Ask(i.timeHeader() + note + req.Prompt)
}

func (i *Invoker) systemPrompt(chatID int64, threadID int) string {
	parts := []string{telegram.Prompt, i.timezoneLine()}
	if i.opts.TaskList != nil {
		parts = append(parts, i.opts.TaskList(chatID, threadID))
	}
	if skills := i.skillsPrompt(); skills != "" {
		parts = append(parts, skills)
	}
	return strings.Join(parts, "\n\n")
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
