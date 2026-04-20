package copilot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	binwrapper "github.com/nickalie/go-binwrapper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	c := New()
	assert.NotNil(t, c)
	assert.NotNil(t, c.bin)
}

func TestBuilderChaining(t *testing.T) {
	c := New()

	// Interface-returning methods (cli.Client) return the same underlying instance.
	assert.Same(t, c, c.Dir("/tmp").(*Copilot))
	assert.Same(t, c, c.AppendSystemPrompt("extra").(*Copilot))
	assert.Same(t, c, c.SkipPermissions().(*Copilot))
}

func TestDir(t *testing.T) {
	c := New()
	c.Dir("/work")
	assert.Equal(t, "/work", c.dir)
}

func TestSkipPermissions(t *testing.T) {
	c := New()
	assert.False(t, c.skipPermissions)
	c.SkipPermissions()
	assert.True(t, c.skipPermissions)
}

func TestAppendSystemPrompt(t *testing.T) {
	c := New()
	c.AppendSystemPrompt("custom instructions")
	assert.Equal(t, "custom instructions", c.systemPrompt)
}

func TestPrepare_AskArgs(t *testing.T) {
	c := New()
	c.skipPermissions = true

	c.prepare()

	args := c.bin.Args()
	assert.Contains(t, args, "-s")
	assert.Contains(t, args, "--output-format=json")
	assert.Contains(t, args, "--allow-all")
	assert.Contains(t, args, "--no-ask-user")
	assert.Contains(t, args, "--autopilot")
	assert.Contains(t, args, "-p")
	assert.NotContains(t, args, "--continue")
}

func TestPrepare_AskArgs_NoSkipPermissions(t *testing.T) {
	c := New()

	c.prepare()

	args := c.bin.Args()
	assert.Contains(t, args, "-s")
	assert.Contains(t, args, "--output-format=json")
	assert.Contains(t, args, "-p")
	assert.NotContains(t, args, "--allow-all")
	assert.NotContains(t, args, "--no-ask-user")
	assert.NotContains(t, args, "--autopilot")
	assert.NotContains(t, args, "--continue")
}

func TestPrepareContinue_Args(t *testing.T) {
	c := New()
	c.skipPermissions = true

	c.prepareContinue()

	args := c.bin.Args()
	assert.Contains(t, args, "--continue")
	assert.Contains(t, args, "-s")
	assert.Contains(t, args, "--output-format=json")
	assert.Contains(t, args, "--allow-all")
	assert.Contains(t, args, "--no-ask-user")
	assert.Contains(t, args, "--autopilot")
	assert.Contains(t, args, "-p")
}

func TestPrepareContinue_NoSkipPermissions(t *testing.T) {
	c := New()

	c.prepareContinue()

	args := c.bin.Args()
	assert.Contains(t, args, "--continue")
	assert.Contains(t, args, "-s")
	assert.Contains(t, args, "--output-format=json")
	assert.Contains(t, args, "-p")
	assert.NotContains(t, args, "--allow-all")
	assert.NotContains(t, args, "--no-ask-user")
	assert.NotContains(t, args, "--autopilot")
}

func TestPrepare_ResetsBetweenCalls(t *testing.T) {
	c := New()
	c.skipPermissions = true

	c.prepare()
	firstArgs := make([]string, len(c.bin.Args()))
	copy(firstArgs, c.bin.Args())

	// Switch to continue mode — should reset and rebuild.
	c.prepareContinue()
	secondArgs := c.bin.Args()

	assert.NotContains(t, firstArgs, "--continue")
	assert.Contains(t, secondArgs, "--continue")
}

func TestWriteSystemPrompt(t *testing.T) {
	dir := t.TempDir()
	c := New()
	c.dir = dir
	c.systemPrompt = "Test instructions"

	err := c.writeSystemPrompt()
	require.NoError(t, err)

	content, err := os.ReadFile(filepath.Join(dir, ".github", "copilot-instructions.md"))
	require.NoError(t, err)
	assert.Equal(t, "Test instructions", string(content))
}

func TestWriteSystemPrompt_CreatesGitHubDir(t *testing.T) {
	dir := t.TempDir()
	c := New()
	c.dir = dir
	c.systemPrompt = "Test instructions"

	err := c.writeSystemPrompt()
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(dir, ".github"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestWriteSystemPrompt_NoPrompt(t *testing.T) {
	dir := t.TempDir()
	c := New()
	c.dir = dir

	err := c.writeSystemPrompt()
	require.NoError(t, err)

	// Should not create the .github directory or file.
	_, err = os.Stat(filepath.Join(dir, ".github"))
	assert.True(t, os.IsNotExist(err))
}

func TestWriteSystemPrompt_NoDir(t *testing.T) {
	c := New()
	c.systemPrompt = "Test instructions"

	err := c.writeSystemPrompt()
	require.NoError(t, err)
}

func TestNewProvider(t *testing.T) {
	p := NewProvider("")
	assert.NotNil(t, p)
	assert.Equal(t, "copilot", p.Name())
}

func TestNewProvider_WithModel(t *testing.T) {
	p := NewProvider("gpt-5.2")
	assert.Equal(t, "gpt-5.2", p.model)
}

func TestProvider_NewClient(t *testing.T) {
	p := NewProvider("")
	client := p.NewClient()
	assert.NotNil(t, client)

	c, ok := client.(*Copilot)
	assert.True(t, ok)
	assert.NotNil(t, c.bin)
}

func TestProvider_NewClient_PropagatesModel(t *testing.T) {
	p := NewProvider("claude-sonnet-4.6")
	client := p.NewClient()
	c, ok := client.(*Copilot)
	require.True(t, ok)
	assert.Equal(t, "claude-sonnet-4.6", c.model)
}

func TestProvider_PreInvoke(t *testing.T) {
	p := NewProvider("")
	assert.NoError(t, p.PreInvoke())
}

func TestNew_DefaultFields(t *testing.T) {
	c := New()
	assert.Equal(t, "", c.dir)
	assert.Equal(t, "", c.model)
	assert.Equal(t, "", c.systemPrompt)
	assert.False(t, c.skipPermissions)
}

func TestAddCommonArgs_WithModel(t *testing.T) {
	c := New()
	c.model = "gpt-5.2"

	c.prepare()

	args := c.bin.Args()
	assert.Contains(t, args, "--model=gpt-5.2")
}

func TestAddCommonArgs_NoModel(t *testing.T) {
	c := New()

	c.prepare()

	args := c.bin.Args()
	for _, a := range args {
		assert.False(t, strings.HasPrefix(a, "--model="), "unexpected --model arg: %s", a)
	}
}

func TestSaveAndLoadSessionID(t *testing.T) {
	dir := t.TempDir()
	c := New()
	c.dir = dir

	c.saveSessionID("abc-123")
	assert.Equal(t, "abc-123", c.loadSessionID())
}

func TestLoadSessionID_NoFile(t *testing.T) {
	c := New()
	c.dir = t.TempDir()
	assert.Equal(t, "", c.loadSessionID())
}

func TestLoadSessionID_NoDir(t *testing.T) {
	c := New()
	assert.Equal(t, "", c.loadSessionID())
}

func TestSaveSessionID_NoDir(t *testing.T) {
	c := New()
	c.saveSessionID("abc-123")
}

func TestPrepareContinue_UsesResumeWhenSessionIDExists(t *testing.T) {
	dir := t.TempDir()
	c := New()
	c.dir = dir
	c.saveSessionID("session-xyz")

	c.prepareContinue()

	args := c.bin.Args()
	assert.Contains(t, args, "--resume=session-xyz")
	assert.NotContains(t, args, "--continue")
}

func TestPrepareContinue_FallsBackToContinueWhenNoSessionID(t *testing.T) {
	c := New()
	c.dir = t.TempDir()

	c.prepareContinue()

	args := c.bin.Args()
	assert.Contains(t, args, "--continue")
	assert.NotContains(t, args, "--resume=session-xyz")
}

// TestHelperProcess provides a fake "copilot" binary for runAndParse tests.
// It is invoked as a subprocess via binwrapper when GO_COPILOT_HELPER=1.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_COPILOT_HELPER") != "1" {
		return
	}

	switch os.Getenv("GO_COPILOT_HELPER_MODE") {
	case "json_no_messages":
		fmt.Fprintln(os.Stdout, `{"type":"session.start","ephemeral":true,"id":"s1","timestamp":"t"}`)
		fmt.Fprint(os.Stderr, "authentication failed")
		os.Exit(1)
	case "json_with_messages":
		fmt.Fprintln(os.Stdout, `{"type":"assistant.message","data":{"content":"partial result"},"id":"1","timestamp":"t"}`)
		fmt.Fprintln(os.Stdout, `{"type":"result","timestamp":"t","sessionId":"test-sess-abc","exitCode":0}`)
		os.Exit(1)
	}

	os.Exit(1)
}

// newWithHelperExec creates a Copilot that uses the running test binary as a fake copilot binary.
func newWithHelperExec(t *testing.T, mode string) *Copilot {
	t.Helper()

	exe, err := os.Executable()
	require.NoError(t, err)

	t.Setenv("GO_COPILOT_HELPER", "1")
	t.Setenv("GO_COPILOT_HELPER_MODE", mode)

	return &Copilot{
		bin: binwrapper.NewBinWrapper().
			ExecPath(exe).
			Arg("-test.run=TestHelperProcess"),
		dir: t.TempDir(),
	}
}

func TestRunAndParse_ErrorReturnsRawTextWhenNoAssistantMessages(t *testing.T) {
	c := newWithHelperExec(t, "json_no_messages")

	result, err := c.runAndParse("query")

	require.Error(t, err)
	assert.Contains(t, result.Text, "session.start")
}

func TestRunAndParse_ErrorReturnsPartialResultWhenMessagesPresent(t *testing.T) {
	c := newWithHelperExec(t, "json_with_messages")

	result, err := c.runAndParse("query")

	require.Error(t, err)
	assert.Equal(t, "partial result", result.Text)
	assert.Equal(t, "test-sess-abc", c.loadSessionID())
}
