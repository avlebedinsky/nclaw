package claude

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const fakeLogin = `#!/bin/sh
echo "$@" > "$(dirname "$0")/args"
echo "Opening browser to sign in…"
echo "If the browser didn't open, visit: https://claude.example/oauth/authorize?code=true&state=abc"
printf 'Paste code here if prompted > '
read code
if [ "$code" = "good#state" ]; then
  echo "Login successful."
  exit 0
fi
echo "Login failed: Request failed with status code 400"
exit 1
`

func fakeClaude(t *testing.T, script string) (*Provider, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script stands in for the CLI")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "claude")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	return NewProvider(path), dir
}

func TestStartLogin_SubmitsCode(t *testing.T) {
	p, dir := fakeClaude(t, fakeLogin)
	p.SetLoginEmail("me@example.com")

	s, err := p.StartLogin(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "https://claude.example/oauth/authorize?code=true&state=abc", s.URL())

	require.NoError(t, s.Submit("  good#state\n"))
	args, err := os.ReadFile(filepath.Join(dir, "args"))
	require.NoError(t, err)
	assert.Equal(t, "auth login --claudeai --email me@example.com", strings.TrimSpace(string(args)))
}

func TestStartLogin_ReportsRejectedCode(t *testing.T) {
	p, _ := fakeClaude(t, fakeLogin)

	s, err := p.StartLogin(context.Background())
	require.NoError(t, err)

	err = s.Submit("wrong")
	require.Error(t, err)
	assert.Equal(t, "claude: Login failed: Request failed with status code 400", err.Error())
}

func TestStartLogin_ExitWithoutLink(t *testing.T) {
	p, _ := fakeClaude(t, "#!/bin/sh\necho 'error: unknown option --claudeai' >&2\nexit 1\n")

	_, err := p.StartLogin(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown option --claudeai")
}

func TestStartLogin_NoLinkInTime(t *testing.T) {
	p, _ := fakeClaude(t, "#!/bin/sh\nsleep 30\n")
	orig := loginURLTimeout
	loginURLTimeout = 200 * time.Millisecond
	t.Cleanup(func() { loginURLTimeout = orig })

	start := time.Now()
	_, err := p.StartLogin(context.Background())
	require.Error(t, err)
	assert.Less(t, time.Since(start), 10*time.Second)
}

func TestStartLogin_ContextEndsSession(t *testing.T) {
	p, _ := fakeClaude(t, fakeLogin)
	ctx, cancel := context.WithCancel(context.Background())

	s, err := p.StartLogin(ctx)
	require.NoError(t, err)
	cancel()

	assert.Error(t, s.Submit("good#state"))
}
