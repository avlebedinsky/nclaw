package gemini

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nickalie/nclaw/internal/cli"
	"github.com/nickalie/nclaw/internal/cli/procrun"
)

// Compile-time check: *Gemini implements cli.Client.
var _ cli.Client = (*Gemini)(nil)

// Gemini wraps the Google Gemini CLI binary.
type Gemini struct {
	bin             *procrun.Cmd
	ctx             context.Context
	dir             string
	systemPrompt    string
	skipPermissions bool
}

// New creates a new Gemini CLI wrapper.
func New() *Gemini {
	bin := procrun.New().
		ExecPath("gemini").
		AutoExe()

	return &Gemini{bin: bin}
}

// Dir sets the working directory for the gemini process.
func (g *Gemini) Dir(dir string) cli.Client {
	g.dir = dir
	return g
}

// SkipPermissions enables --approval-mode yolo (auto-approve all actions).
func (g *Gemini) SkipPermissions() cli.Client {
	g.skipPermissions = true
	return g
}

// Context sets the context that cancels the run.
func (g *Gemini) Context(ctx context.Context) cli.Client {
	g.ctx = ctx
	return g
}

func (g *Gemini) runCtx() context.Context {
	if g.ctx == nil {
		return context.Background()
	}
	return g.ctx
}

// AppendSystemPrompt sets a system prompt to be written to GEMINI.md
// in the working directory before invocation.
func (g *Gemini) AppendSystemPrompt(prompt string) cli.Client {
	g.systemPrompt = prompt
	return g
}

// Ask sends a query and returns the parsed stream-json response.
func (g *Gemini) Ask(query string) (*cli.Result, error) {
	if err := g.writeSystemPrompt(); err != nil {
		return &cli.Result{}, fmt.Errorf("gemini: write system prompt: %w", err)
	}

	g.prepare()
	return g.runAndParse(query)
}

// Continue sends a query resuming the most recent session.
func (g *Gemini) Continue(query string) (*cli.Result, error) {
	if err := g.writeSystemPrompt(); err != nil {
		return &cli.Result{}, fmt.Errorf("gemini: write system prompt: %w", err)
	}

	g.prepareContinue()
	return g.runAndParse(query)
}

// runAndParse executes the CLI and parses stream-json output into a Result.
func (g *Gemini) runAndParse(query string) (*cli.Result, error) {
	if err := g.bin.Run(g.runCtx(), query); err != nil {
		result := parseStreamJSONOutput(g.bin.StdOut())
		if result.Text == "" && result.FullText == "" {
			text := strings.TrimSpace(string(g.bin.CombinedOutput()))
			result = &cli.Result{Text: text, FullText: text}
		}

		return result, fmt.Errorf("gemini: %w", err)
	}

	return parseStreamJSONOutput(g.bin.StdOut()), nil
}

// Version returns the Gemini CLI version string.
func (g *Gemini) Version() (string, error) {
	g.bin.Reset()

	ctx, cancel := context.WithTimeout(context.Background(), cli.VersionTimeout)
	defer cancel()

	if err := g.bin.Run(ctx, "--version"); err != nil {
		return strings.TrimSpace(string(g.bin.CombinedOutput())), fmt.Errorf("gemini: %w", err)
	}

	return strings.TrimSpace(string(g.bin.StdOut())), nil
}

// writeSystemPrompt writes the system prompt to GEMINI.md in the working directory.
func (g *Gemini) writeSystemPrompt() error {
	if g.systemPrompt == "" || g.dir == "" {
		return nil
	}

	path := filepath.Join(g.dir, "GEMINI.md")
	return os.WriteFile(path, []byte(g.systemPrompt), 0o644)
}

// prepare resets the command and rebuilds arguments for an Ask call.
func (g *Gemini) prepare() {
	g.bin.Reset()
	g.addCommonArgs()
}

// prepareContinue resets the command and rebuilds arguments for a Continue call.
func (g *Gemini) prepareContinue() {
	g.bin.Reset()
	g.bin.Arg("--resume", "latest")
	g.addCommonArgs()
}

// addCommonArgs adds flags shared between Ask and Continue.
func (g *Gemini) addCommonArgs() {
	if g.dir != "" {
		g.bin.Dir(g.dir)
	}

	g.bin.Arg("--output-format", "stream-json")

	if g.skipPermissions {
		g.bin.Arg("--approval-mode", "yolo")
	}

	g.bin.Arg("-p")
}
