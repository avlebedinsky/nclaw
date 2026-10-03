package claude

import "github.com/nickalie/nclaw/internal/cli"

// Provider implements cli.Provider for the Claude Code CLI backend.
type Provider struct {
	execPath string
}

// Compile-time checks: *Provider implements cli.Provider and its optional capabilities.
var (
	_ cli.Provider             = (*Provider)(nil)
	_ cli.NativeSkillsProvider = (*Provider)(nil)
	_ cli.SessionStore         = (*Provider)(nil)
	_ cli.AuthProvider         = (*Provider)(nil)
)

// NewProvider creates a new Claude CLI provider.
func NewProvider(execPath ...string) *Provider {
	p := &Provider{}
	if len(execPath) > 0 {
		p.execPath = execPath[0]
	}
	return p
}

// NewClient creates a new Claude CLI client.
func (p *Provider) NewClient() cli.Client {
	return p.newClaude()
}

// PreInvoke refreshes the OAuth token if needed before each CLI invocation.
func (p *Provider) PreInvoke() error {
	return EnsureValidToken()
}

// Version returns the Claude CLI version string.
func (p *Provider) Version() (string, error) {
	return p.newClaude().Version()
}

// Name returns the backend name.
func (p *Provider) Name() string {
	return "claude"
}

func (p *Provider) newClaude() *Claude {
	c := New()
	if p.execPath != "" {
		c.ExecPath(p.execPath)
	}
	return c
}

// NativeSkills reports that the CLI loads skills from its own skills directory.
func (p *Provider) NativeSkills() bool {
	return true
}

// SessionSize returns the size of the latest session transcript for dir.
func (p *Provider) SessionSize(dir string) (int64, bool) {
	return SessionSize(dir)
}

// ArchiveSession archives the session history for dir so the next run starts fresh.
func (p *Provider) ArchiveSession(dir string) error {
	return ArchiveSession(dir)
}
