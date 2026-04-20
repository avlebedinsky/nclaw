package copilot

import "github.com/nickalie/nclaw/internal/cli"

// Provider implements cli.Provider for the GitHub Copilot CLI backend.
type Provider struct {
	model string
}

// Compile-time check: *Provider implements cli.Provider.
var _ cli.Provider = (*Provider)(nil)

// NewProvider creates a new Copilot CLI provider.
func NewProvider(model string) *Provider {
	return &Provider{model: model}
}

// NewClient creates a new Copilot CLI client.
func (p *Provider) NewClient() cli.Client {
	c := New()
	c.model = p.model
	return c
}

// PreInvoke is a no-op for Copilot (no token refresh needed).
func (p *Provider) PreInvoke() error {
	return nil
}

// Version returns the Copilot CLI version string.
func (p *Provider) Version() (string, error) {
	return New().Version()
}

// Name returns the backend name.
func (p *Provider) Name() string {
	return "copilot"
}
