package claudish

import (
	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/cli/claude"
)

// Provider implements cli.Provider for the claudish CLI backend.
type Provider struct {
	model         string
	modelOpus     string
	modelSonnet   string
	modelHaiku    string
	modelSubagent string
}

// Compile-time checks: *Provider implements cli.Provider and its optional capabilities.
var (
	_ cli.Provider             = (*Provider)(nil)
	_ cli.NativeSkillsProvider = (*Provider)(nil)
	_ cli.SessionStore         = (*Provider)(nil)
	_ cli.MemoryFileProvider   = (*Provider)(nil)
)

// NewProvider creates a new claudish CLI provider with model configuration.
func NewProvider(model, modelOpus, modelSonnet, modelHaiku, modelSubagent string) *Provider {
	return &Provider{
		model:         model,
		modelOpus:     modelOpus,
		modelSonnet:   modelSonnet,
		modelHaiku:    modelHaiku,
		modelSubagent: modelSubagent,
	}
}

// NewClient creates a new claudish CLI client with model config pre-set.
func (p *Provider) NewClient() cli.Client {
	c := New()
	c.model = p.model
	c.modelOpus = p.modelOpus
	c.modelSonnet = p.modelSonnet
	c.modelHaiku = p.modelHaiku
	c.modelSubagent = p.modelSubagent
	return c
}

// PreInvoke is a no-op for claudish (it manages its own proxy/auth).
func (p *Provider) PreInvoke() error {
	return nil
}

// Version returns the claudish CLI version string.
func (p *Provider) Version() (string, error) {
	return New().Version()
}

// Name returns the backend name.
func (p *Provider) Name() string {
	return "claudish"
}

// MemoryFile names the instructions file Claude Code loads from the working directory and its parents.
func (p *Provider) MemoryFile() string {
	return "CLAUDE.md"
}

// NativeSkills reports that the CLI loads skills from its own skills directory.
func (p *Provider) NativeSkills() bool {
	return true
}

// SessionSize returns the size of the latest session transcript for dir.
func (p *Provider) SessionSize(dir string) (int64, bool) {
	return claude.SessionSize(dir)
}

// ArchiveSession archives the session history for dir so the next run starts fresh.
func (p *Provider) ArchiveSession(dir string) error {
	return claude.ArchiveSession(dir)
}
