package copilot

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nickalie/go-binwrapper"
	"github.com/nickalie/nclaw/internal/cli"
)

// Compile-time check: *Copilot implements cli.Client.
var _ cli.Client = (*Copilot)(nil)

// sessionIDFile is the filename used to persist the Copilot session ID in the chat directory.
const sessionIDFile = ".copilot-session-id"

// Copilot wraps the GitHub Copilot CLI binary.
type Copilot struct {
	bin             *binwrapper.BinWrapper
	dir             string
	model           string
	systemPrompt    string
	skipPermissions bool
}

// New creates a new Copilot CLI wrapper.
func New() *Copilot {
	bin := binwrapper.NewBinWrapper().
		ExecPath("copilot").
		AutoExe()

	return &Copilot{bin: bin}
}

// Dir sets the working directory for the copilot process.
func (c *Copilot) Dir(dir string) cli.Client {
	c.dir = dir
	return c
}

// SkipPermissions enables --allow-all, --no-ask-user, and --autopilot flags.
func (c *Copilot) SkipPermissions() cli.Client {
	c.skipPermissions = true
	return c
}

// AppendSystemPrompt sets a system prompt to be written to
// .github/copilot-instructions.md in the working directory before invocation.
func (c *Copilot) AppendSystemPrompt(prompt string) cli.Client {
	c.systemPrompt = prompt
	return c
}

// Ask sends a query and returns the structured response.
func (c *Copilot) Ask(query string) (*cli.Result, error) {
	if err := c.writeSystemPrompt(); err != nil {
		return &cli.Result{}, fmt.Errorf("copilot: write system prompt: %w", err)
	}

	c.prepare()
	return c.runAndParse(query)
}

// Continue sends a query resuming the session.
// It uses --resume=SESSION-ID when a session ID is persisted in the chat directory,
// falling back to --continue (most recent session) otherwise.
func (c *Copilot) Continue(query string) (*cli.Result, error) {
	if err := c.writeSystemPrompt(); err != nil {
		return &cli.Result{}, fmt.Errorf("copilot: write system prompt: %w", err)
	}

	c.prepareContinue()
	return c.runAndParse(query)
}

// runAndParse executes the CLI and parses JSONL output into a Result.
// The session ID from the result event is persisted for future Continue() calls.
func (c *Copilot) runAndParse(query string) (*cli.Result, error) {
	if err := c.bin.Run(query); err != nil {
		parsed := parseJSONOutput(c.bin.StdOut())
		if parsed.result.Text == "" && parsed.result.FullText == "" {
			text := strings.TrimSpace(string(c.bin.CombinedOutput()))
			parsed.result = &cli.Result{Text: text, FullText: text}
		}

		c.saveSessionID(parsed.sessionID)

		return parsed.result, fmt.Errorf("copilot: %w", err)
	}

	parsed := parseJSONOutput(c.bin.StdOut())
	c.saveSessionID(parsed.sessionID)

	return parsed.result, nil
}

// Version returns the Copilot CLI version string.
func (c *Copilot) Version() (string, error) {
	c.bin.Reset()

	if err := c.bin.Run("version"); err != nil {
		return strings.TrimSpace(string(c.bin.CombinedOutput())), fmt.Errorf("copilot: %w", err)
	}

	return strings.TrimSpace(string(c.bin.StdOut())), nil
}

// writeSystemPrompt writes the system prompt to .github/copilot-instructions.md
// in the working directory.
func (c *Copilot) writeSystemPrompt() error {
	if c.systemPrompt == "" || c.dir == "" {
		return nil
	}

	dir := filepath.Join(c.dir, ".github")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create .github dir: %w", err)
	}

	path := filepath.Join(dir, "copilot-instructions.md")
	return os.WriteFile(path, []byte(c.systemPrompt), 0o644)
}

// prepare resets the binwrapper and rebuilds arguments for an Ask call.
func (c *Copilot) prepare() {
	c.bin.Reset()
	c.addCommonArgs()
}

// prepareContinue resets the binwrapper and rebuilds arguments for a Continue call.
// Uses --resume=SESSION-ID when a session ID is available, --continue otherwise.
func (c *Copilot) prepareContinue() {
	c.bin.Reset()
	if id := c.loadSessionID(); id != "" {
		c.bin.Arg("--resume=" + id)
	} else {
		c.bin.Arg("--continue")
	}
	c.addCommonArgs()
}

// addCommonArgs adds flags shared between Ask and Continue.
func (c *Copilot) addCommonArgs() {
	if c.dir != "" {
		c.bin.Dir(c.dir)
	}

	// Silent mode: suppress stats, output only the agent response.
	c.bin.Arg("-s")

	// Structured JSONL output for proper message parsing.
	c.bin.Arg("--output-format=json")

	if c.model != "" {
		c.bin.Arg("--model=" + c.model)
	}

	if c.skipPermissions {
		// --allow-all covers tools, paths, and URLs; --no-ask-user disables
		// the ask_user tool; --autopilot enables autonomous multi-turn continuation.
		c.bin.Arg("--allow-all")
		c.bin.Arg("--no-ask-user")
		c.bin.Arg("--autopilot")
	}

	c.bin.Arg("-p")
}

// loadSessionID reads the persisted session ID from the chat directory.
func (c *Copilot) loadSessionID() string {
	if c.dir == "" {
		return ""
	}

	data, err := os.ReadFile(filepath.Join(c.dir, sessionIDFile))
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(data))
}

// saveSessionID persists the session ID to the chat directory.
func (c *Copilot) saveSessionID(id string) {
	if c.dir == "" || id == "" {
		return
	}

	_ = os.WriteFile(filepath.Join(c.dir, sessionIDFile), []byte(id), 0o644)
}
