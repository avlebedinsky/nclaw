package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthExpiry_ReadsRefreshTokenExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), credentialFile)
	withTestCredPath(t, path)
	require.NoError(t, os.WriteFile(path,
		[]byte(`{"claudeAiOauth":{"accessToken":"a","expiresAt":1,"refreshTokenExpiresAt":1791558360000}}`), 0o600))

	expiry, ok := NewProvider().AuthExpiry()

	require.True(t, ok)
	assert.Equal(t, time.UnixMilli(1791558360000), expiry)
}

func TestAuthExpiry_UnknownWithoutField(t *testing.T) {
	path := filepath.Join(t.TempDir(), credentialFile)
	withTestCredPath(t, path)
	require.NoError(t, os.WriteFile(path, []byte(`{"claudeAiOauth":{"accessToken":"a"}}`), 0o600))

	_, ok := NewProvider().AuthExpiry()
	assert.False(t, ok)
}

func TestAuthExpiry_UnknownWithoutFile(t *testing.T) {
	withTestCredPath(t, filepath.Join(t.TempDir(), credentialFile))

	_, ok := NewProvider().AuthExpiry()
	assert.False(t, ok)
}

func TestAuthExpiry_UnknownWithEnvCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), credentialFile)
	withTestCredPath(t, path)
	require.NoError(t, os.WriteFile(path, []byte(`{"claudeAiOauth":{"refreshTokenExpiresAt":1791558360000}}`), 0o600))
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "token")

	_, ok := NewProvider().AuthExpiry()
	assert.False(t, ok)
}

func TestIsAuthFailure(t *testing.T) {
	p := NewProvider()
	assert.True(t, p.IsAuthFailure("Failed to authenticate: OAuth session expired and could not be refreshed"))
	assert.True(t, p.IsAuthFailure(`API Error: 401 {"type":"error","error":{"type":"authentication_error"}}`))
	assert.True(t, p.IsAuthFailure("Invalid API key · Please run /login"))
	assert.False(t, p.IsAuthFailure("claude: exit status 1"))
	assert.False(t, p.IsAuthFailure("Here is the summary you asked for."))
}
