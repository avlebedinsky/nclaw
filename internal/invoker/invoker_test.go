package invoker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/telegram"
)

type fakeClient struct {
	dir          string
	skipPerms    bool
	systemPrompt string
	query        string
	mode         string
	result       *cli.Result
	err          error
	model        string
}

func (c *fakeClient) UseModel(m string) cli.Client       { c.model = m; return c }
func (c *fakeClient) Dir(dir string) cli.Client          { c.dir = dir; return c }
func (c *fakeClient) Context(context.Context) cli.Client { return c }
func (c *fakeClient) SkipPermissions() cli.Client {
	c.skipPerms = true
	return c
}
func (c *fakeClient) AppendSystemPrompt(p string) cli.Client {
	c.systemPrompt = p
	return c
}
func (c *fakeClient) Ask(q string) (*cli.Result, error) {
	c.query, c.mode = q, "ask"
	return c.result, c.err
}
func (c *fakeClient) Continue(q string) (*cli.Result, error) {
	c.query, c.mode = q, "continue"
	return c.result, c.err
}

type fakeProvider struct {
	client       *fakeClient
	preInvokeErr error
	preInvoked   int
}

func (p *fakeProvider) NewClient() cli.Client    { return p.client }
func (p *fakeProvider) PreInvoke() error         { p.preInvoked++; return p.preInvokeErr }
func (p *fakeProvider) Version() (string, error) { return "1.0", nil }
func (p *fakeProvider) Name() string             { return "fake" }

type nativeProvider struct {
	fakeProvider
	size     int64
	archived []string
}

func (p *nativeProvider) NativeSkills() bool { return true }
func (p *nativeProvider) SessionSize(string) (int64, bool) {
	return p.size, p.size > 0
}
func (p *nativeProvider) ArchiveSession(dir string) error {
	p.archived = append(p.archived, dir)
	return nil
}

var fixedNow = time.Date(2026, 10, 3, 22, 45, 10, 0, time.UTC)

func newTestInvoker(t *testing.T, p cli.Provider, opts Options) *Invoker {
	t.Helper()
	if opts.DataDir == "" {
		opts.DataDir = t.TempDir()
	}
	if opts.Location == nil {
		loc, err := time.LoadLocation("Europe/Moscow")
		require.NoError(t, err)
		opts.Location = loc
	}
	opts.Now = func() time.Time { return fixedNow }
	return New(p, opts)
}

func writeSkill(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "demo"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "demo", "SKILL.md"), []byte("demo skill body"), 0o644))
	return dir
}

func TestRun_ContinuesInChatDirWithSharedPrompt(t *testing.T) {
	client := &fakeClient{result: &cli.Result{Text: "ok"}}
	p := &fakeProvider{client: client}
	inv := newTestInvoker(t, p, Options{
		SkillsDir: writeSkill(t),
		TaskList:  func(chatID int64, threadID int) string { return "Current scheduled tasks: none" },
	})

	out := inv.Run(context.Background(), Request{ChatID: 100, ThreadID: 5, Prompt: "hello"})

	require.NoError(t, out.Err)
	assert.Equal(t, "ok", out.Result.Text)
	assert.Equal(t, inv.ChatDir(100, 5), out.Dir)
	assert.Equal(t, out.Dir, client.dir)
	assert.DirExists(t, out.Dir)
	assert.Equal(t, "continue", client.mode)
	assert.True(t, client.skipPerms)
	assert.Equal(t, 1, p.preInvoked)
	assert.Contains(t, client.systemPrompt, telegram.Prompt)
	assert.Contains(t, client.systemPrompt, runRule)
	assert.Contains(t, client.systemPrompt, "Scheduler timezone: Europe/Moscow (UTC+03:00)")
	assert.Contains(t, client.systemPrompt, "Current scheduled tasks: none")
	assert.Contains(t, client.systemPrompt, "demo skill body")
	assert.Equal(t, "[Current time: Sunday, 2026-10-04 01:45:10 MSK (+03:00)]\n\nhello", client.query)
}

func TestRun_SkipsSkillsForNativeProvider(t *testing.T) {
	client := &fakeClient{result: &cli.Result{}}
	inv := newTestInvoker(t, &nativeProvider{fakeProvider: fakeProvider{client: client}}, Options{SkillsDir: writeSkill(t)})

	inv.Run(context.Background(), Request{ChatID: 1, Prompt: "x"})

	assert.NotContains(t, client.systemPrompt, "demo skill body")
}

func TestRun_NilResultBecomesEmpty(t *testing.T) {
	client := &fakeClient{err: errors.New("boom")}
	inv := newTestInvoker(t, &fakeProvider{client: client}, Options{})

	out := inv.Run(context.Background(), Request{ChatID: 1, Prompt: "x"})

	require.NotNil(t, out.Result)
	assert.EqualError(t, out.Err, "boom")
}

func TestRun_PreInvokeErrorDoesNotBlock(t *testing.T) {
	client := &fakeClient{result: &cli.Result{Text: "ok"}}
	inv := newTestInvoker(t, &fakeProvider{client: client, preInvokeErr: errors.New("refresh failed")}, Options{})

	out := inv.Run(context.Background(), Request{ChatID: 1, Prompt: "x"})

	require.NoError(t, out.Err)
	assert.Equal(t, "ok", out.Result.Text)
}

func TestRun_AppliesConfigure(t *testing.T) {
	client := &fakeClient{result: &cli.Result{}}
	inv := newTestInvoker(t, &fakeProvider{client: client}, Options{})

	var configured cli.Client
	inv.Run(context.Background(), Request{ChatID: 1, Prompt: "x", Configure: func(c cli.Client) { configured = c }})

	assert.Same(t, client, configured)
}

func TestRun_ArchivesOversizedSession(t *testing.T) {
	p := &nativeProvider{fakeProvider: fakeProvider{client: &fakeClient{result: &cli.Result{}}}, size: 150}
	inv := newTestInvoker(t, p, Options{MaxSessionBytes: 100})

	out := inv.Run(context.Background(), Request{ChatID: 1, Prompt: "x"})

	assert.Equal(t, []string{out.Dir}, p.archived)
}

func TestRun_KeepsSessionUnderLimitOrWhenDisabled(t *testing.T) {
	for _, tc := range []struct {
		name  string
		size  int64
		limit int64
	}{{"under limit", 50, 100}, {"disabled", 1 << 30, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			p := &nativeProvider{fakeProvider: fakeProvider{client: &fakeClient{result: &cli.Result{}}}, size: tc.size}
			inv := newTestInvoker(t, p, Options{MaxSessionBytes: tc.limit})

			inv.Run(context.Background(), Request{ChatID: 1, Prompt: "x"})

			assert.Empty(t, p.archived)
		})
	}
}

func TestRun_MkdirFailure(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocker, nil, 0o644))
	client := &fakeClient{result: &cli.Result{Text: "never"}}
	inv := newTestInvoker(t, &fakeProvider{client: client}, Options{DataDir: blocker})

	out := inv.Run(context.Background(), Request{ChatID: 1, Prompt: "x"})

	require.Error(t, out.Err)
	assert.Empty(t, client.mode)
	assert.NotNil(t, out.Result)
}

func TestLoadSkillsPrompt(t *testing.T) {
	dir := writeSkill(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("not a skill"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "empty"), 0o755))

	prompt := loadSkillsPrompt(dir)

	assert.Equal(t, "# Available Skills\n\ndemo skill body", prompt)
	assert.Empty(t, loadSkillsPrompt(filepath.Join(dir, "missing")))
}

type ephemeralClient struct {
	fakeClient
	ephemeral bool
}

func (c *ephemeralClient) Context(context.Context) cli.Client { return c }
func (c *ephemeralClient) Dir(dir string) cli.Client          { c.fakeClient.Dir(dir); return c }
func (c *ephemeralClient) SkipPermissions() cli.Client {
	c.fakeClient.SkipPermissions()
	return c
}
func (c *ephemeralClient) AppendSystemPrompt(p string) cli.Client {
	c.fakeClient.AppendSystemPrompt(p)
	return c
}
func (c *ephemeralClient) Ephemeral() cli.Client {
	c.ephemeral = true
	return c
}

type ephemeralProvider struct {
	nativeProvider
	client *ephemeralClient
}

func (p *ephemeralProvider) NewClient() cli.Client { return p.client }

func TestRun_IsolatedAsksInSubdirWithoutPersistence(t *testing.T) {
	client := &ephemeralClient{fakeClient: fakeClient{result: &cli.Result{Text: "ok"}}}
	p := &ephemeralProvider{client: client, nativeProvider: nativeProvider{size: 1 << 30}}
	inv := newTestInvoker(t, p, Options{MaxSessionBytes: 100})

	out := inv.Run(context.Background(), Request{ChatID: 100, Prompt: "reminder", Mode: Isolated})

	assert.Equal(t, filepath.Join(inv.ChatDir(100, 0), IsolatedDirName), out.Dir)
	assert.Equal(t, inv.ChatDir(100, 0), out.ChatDir)
	assert.DirExists(t, out.Dir)
	assert.Equal(t, "ask", client.mode)
	assert.Equal(t, out.Dir, client.dir)
	assert.True(t, client.ephemeral)
	assert.Contains(t, client.query, "[Isolated run in "+out.Dir)
	assert.True(t, strings.HasSuffix(client.query, "reminder"))
	assert.Empty(t, p.archived, "isolated runs must not reset the chat session")
}

func TestRun_IsolatedWithoutEphemeralSupportStillAsks(t *testing.T) {
	client := &fakeClient{result: &cli.Result{}}
	inv := newTestInvoker(t, &fakeProvider{client: client}, Options{})

	inv.Run(context.Background(), Request{ChatID: 1, Prompt: "x", Mode: Isolated})

	assert.Equal(t, "ask", client.mode)
}

type blockingClient struct {
	fakeClient
	ctx context.Context
}

func (c *blockingClient) Context(ctx context.Context) cli.Client { c.ctx = ctx; return c }
func (c *blockingClient) Dir(string) cli.Client                  { return c }
func (c *blockingClient) SkipPermissions() cli.Client            { return c }
func (c *blockingClient) AppendSystemPrompt(string) cli.Client   { return c }
func (c *blockingClient) Continue(string) (*cli.Result, error) {
	<-c.ctx.Done()
	return &cli.Result{Text: "partial"}, fmt.Errorf("claude: %w", context.Cause(c.ctx))
}

type blockingProvider struct {
	fakeProvider
	client *blockingClient
}

func (p *blockingProvider) NewClient() cli.Client { return p.client }

func TestRun_TimeoutStopsRun(t *testing.T) {
	inv := newTestInvoker(t, &blockingProvider{client: &blockingClient{}}, Options{Timeout: 50 * time.Millisecond})

	out := inv.Run(context.Background(), Request{ChatID: 1, Prompt: "x"})

	var timeout *TimeoutError
	require.ErrorAs(t, out.Err, &timeout)
	assert.Equal(t, 50*time.Millisecond, timeout.After)
	assert.Equal(t, "partial", out.Result.Text)
}

func TestRun_ParentCancelStopsRun(t *testing.T) {
	inv := newTestInvoker(t, &blockingProvider{client: &blockingClient{}}, Options{})
	ctx, cancel := context.WithCancelCause(context.Background())
	stop := errors.New("stopped")

	done := make(chan Outcome, 1)
	go func() { done <- inv.Run(ctx, Request{ChatID: 1, Prompt: "x"}) }()
	cancel(stop)

	select {
	case out := <-done:
		assert.ErrorIs(t, out.Err, stop)
	case <-time.After(5 * time.Second):
		t.Fatal("run was not canceled")
	}
}

func TestResetSession_ArchivesWithSessionStore(t *testing.T) {
	p := &nativeProvider{fakeProvider: fakeProvider{client: &fakeClient{result: &cli.Result{}}}}
	inv := newTestInvoker(t, p, Options{})

	require.NoError(t, inv.ResetSession(5, 1))

	assert.Equal(t, []string{inv.ChatDir(5, 1)}, p.archived)
}

func TestResetSession_MarkerStartsFreshOnce(t *testing.T) {
	client := &fakeClient{result: &cli.Result{Text: "ok"}}
	inv := newTestInvoker(t, &fakeProvider{client: client}, Options{})

	require.NoError(t, inv.ResetSession(5, 0))
	inv.Run(context.Background(), Request{ChatID: 5, Prompt: "first"})
	assert.Equal(t, "ask", client.mode)

	inv.Run(context.Background(), Request{ChatID: 5, Prompt: "second"})
	assert.Equal(t, "continue", client.mode)
}

func TestResetSession_MarkerKeptWhenRunFails(t *testing.T) {
	client := &fakeClient{err: errors.New("boom")}
	inv := newTestInvoker(t, &fakeProvider{client: client}, Options{})

	require.NoError(t, inv.ResetSession(5, 0))
	inv.Run(context.Background(), Request{ChatID: 5, Prompt: "x"})
	inv.Run(context.Background(), Request{ChatID: 5, Prompt: "y"})

	assert.Equal(t, "ask", client.mode)
}

func TestSessionSize(t *testing.T) {
	inv := newTestInvoker(t, &nativeProvider{size: 42}, Options{MaxSessionBytes: 100})
	size, ok := inv.SessionSize(1, 0)
	assert.True(t, ok)
	assert.Equal(t, int64(42), size)
	assert.Equal(t, int64(100), inv.MaxSessionBytes())

	_, ok = newTestInvoker(t, &fakeProvider{}, Options{}).SessionSize(1, 0)
	assert.False(t, ok)
}

type memoryProvider struct {
	fakeProvider
}

func (p *memoryProvider) MemoryFile() string { return "CLAUDE.md" }

func TestRun_NamesPrivateChatPerson(t *testing.T) {
	client := &fakeClient{result: &cli.Result{}}
	inv := newTestInvoker(t, &memoryProvider{fakeProvider{client: client}}, Options{})
	_, err := telegram.SaveChatName(inv.ChatDir(396429376, 0), "Анна")
	require.NoError(t, err)

	inv.Run(context.Background(), Request{ChatID: 396429376, Prompt: "x"})

	assert.Contains(t, client.systemPrompt, "This is a private Telegram chat with Анна.")
	assert.NotContains(t, client.systemPrompt, "auto memory")
}

func TestRun_NamesGroupTopicAndSharedMemory(t *testing.T) {
	client := &fakeClient{result: &cli.Result{}}
	inv := newTestInvoker(t, &memoryProvider{fakeProvider{client: client}}, Options{})
	_, err := telegram.SaveChatName(inv.ChatDir(-100123, 0), "Семья")
	require.NoError(t, err)
	_, err = telegram.SaveTopicName(inv.ChatDir(-100123, 1224), "Финансы")
	require.NoError(t, err)

	inv.Run(context.Background(), Request{ChatID: -100123, ThreadID: 1224, Prompt: "x"})

	assert.Contains(t, client.systemPrompt, "This is the Telegram group «Семья». Messages from people start with [From: name].")
	assert.Contains(t, client.systemPrompt, "This conversation is the topic «Финансы» of that chat")
	assert.Contains(t, client.systemPrompt, "go into "+filepath.Join(inv.ChatDir(-100123, 0), "CLAUDE.md"))
}

func TestRun_SharedMemoryOnlyForProvidersThatLoadIt(t *testing.T) {
	client := &fakeClient{result: &cli.Result{}}
	inv := newTestInvoker(t, &fakeProvider{client: client}, Options{})

	inv.Run(context.Background(), Request{ChatID: -100123, ThreadID: 5, Prompt: "x"})

	assert.Contains(t, client.systemPrompt, "This is a Telegram group chat.")
	assert.NotContains(t, client.systemPrompt, "CLAUDE.md")
	assert.NotContains(t, client.systemPrompt, "This conversation is the topic")
}

func TestRun_PrivateChatTopicGetsSharedMemory(t *testing.T) {
	client := &fakeClient{result: &cli.Result{}}
	inv := newTestInvoker(t, &memoryProvider{fakeProvider{client: client}}, Options{})

	inv.Run(context.Background(), Request{ChatID: 375321681, ThreadID: 9, Prompt: "x"})

	assert.Contains(t, client.systemPrompt, "This is a private Telegram chat.")
	assert.Contains(t, client.systemPrompt, filepath.Join(inv.ChatDir(375321681, 0), "CLAUDE.md"))
}

type modelProvider struct {
	fakeProvider
}

func (p *modelProvider) Models() []string { return []string{"opus", "sonnet"} }

func TestSetModel_IsUsedByTheChatsRunsOnly(t *testing.T) {
	client := &fakeClient{result: &cli.Result{Text: "ok"}}
	inv := newTestInvoker(t, &modelProvider{fakeProvider{client: client}}, Options{})
	assert.Equal(t, []string{"opus", "sonnet"}, inv.Models())
	assert.Empty(t, inv.Model(100, 5))

	require.NoError(t, inv.SetModel(100, 5, "opus"))
	assert.Equal(t, "opus", inv.Model(100, 5))
	inv.Run(context.Background(), Request{ChatID: 100, ThreadID: 5, Prompt: "hi"})
	assert.Equal(t, "opus", client.model)

	client.model = ""
	inv.Run(context.Background(), Request{ChatID: 100, ThreadID: 6, Prompt: "hi"})
	assert.Empty(t, client.model)

	require.NoError(t, inv.SetModel(100, 5, ""))
	require.NoError(t, inv.SetModel(100, 5, ""))
	assert.Empty(t, inv.Model(100, 5))
	assert.Nil(t, newTestInvoker(t, &fakeProvider{client: client}, Options{}).Models())
}
