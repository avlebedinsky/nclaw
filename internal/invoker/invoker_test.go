package invoker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
}

func (c *fakeClient) Dir(dir string) cli.Client { c.dir = dir; return c }
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
	return New(p, telegram.NewChatLocker(), opts)
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
