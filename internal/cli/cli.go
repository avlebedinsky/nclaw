package cli

import (
	"context"
	"time"
)

// Result holds the output from a CLI invocation.
type Result struct {
	// Text is the final assistant message (suitable for display).
	Text string
	// FullText contains all assistant messages concatenated.
	// Useful for scanning command blocks (sendfile, schedule, webhook)
	// that may appear in non-final messages during multi-turn execution.
	FullText string
	// Messages holds each individual assistant message in order. Used when
	// stream-message output is enabled to deliver every message separately
	// instead of only the final one. May be empty for backends or code paths
	// that produce a single message (consumers fall back to Text).
	Messages []string
}

// VersionTimeout bounds a CLI version probe.
const VersionTimeout = 30 * time.Second

// Client is a per-request builder for invoking a CLI backend.
type Client interface {
	Context(ctx context.Context) Client
	Dir(dir string) Client
	SkipPermissions() Client
	AppendSystemPrompt(prompt string) Client
	Ask(query string) (*Result, error)
	Continue(query string) (*Result, error)
}

// MessageHandler receives each assistant message as soon as it is parsed from a
// backend's streaming output. It is invoked sequentially, in order, while the
// CLI process is still running.
type MessageHandler func(message string)

// StreamingClient is an optional interface a Client may implement to deliver
// assistant messages incrementally as they arrive, enabling real-time output
// instead of a single batched result at the end. Backends that cannot stream
// (plain-text output) simply do not implement it.
type StreamingClient interface {
	OnMessage(handler MessageHandler) Client
}

// Provider is a singleton per backend that creates clients and handles
// backend-specific lifecycle tasks.
type Provider interface {
	NewClient() Client
	PreInvoke() error // e.g., token refresh; no-op for codex/copilot
	Version() (string, error)
	Name() string
}

// NativeSkillsProvider is implemented by providers whose CLI loads skills from its
// own skills directory, so nclaw does not need to inject them into the prompt.
type NativeSkillsProvider interface {
	NativeSkills() bool
}

// SessionStore is implemented by providers that can measure and archive the
// persisted session of a working directory.
type SessionStore interface {
	SessionSize(dir string) (int64, bool)
	ArchiveSession(dir string) error
}

// MemoryFileProvider is implemented by providers whose CLI loads an instructions file with
// this name from the working directory and from every directory above it.
type MemoryFileProvider interface {
	MemoryFile() string
}

// AutoMemoryStore is implemented by providers whose CLI keeps an automatic memory per
// working directory.
type AutoMemoryStore interface {
	AutoMemoryDir(dir string) (string, error)
	// ClearAutoMemory moves the automatic memory of dir aside, so the next run starts without it.
	ClearAutoMemory(dir string) error
}

// ContextUsage is how full a conversation's context was at its last reply.
type ContextUsage struct {
	Tokens int
	// CompactAt is the size at which the CLI compacts the conversation by itself; 0 when unknown.
	CompactAt int
	// Model is the model that wrote the last reply.
	Model string
}

// ContextProvider is implemented by providers that can tell how full a conversation's
// context is and compact it on request.
type ContextProvider interface {
	ContextUsage(dir string) (ContextUsage, bool)
	// CompactPrompt is the prompt that makes the CLI compact the conversation.
	CompactPrompt() string
}

// AuthProvider is implemented by providers whose stored sign-in expires on a known date.
type AuthProvider interface {
	// AuthExpiry returns when the stored sign-in stops working; ok is false when it is unknown.
	AuthExpiry() (expiry time.Time, ok bool)
	// IsAuthFailure reports whether the output of a failed run is the backend's sign-in error.
	IsAuthFailure(output string) bool
}

// LoginProvider is implemented by providers that can sign in again interactively.
type LoginProvider interface {
	// StartLogin starts a sign-in that lives until ctx is done and returns once its link is known.
	StartLogin(ctx context.Context) (LoginSession, error)
}

// LoginSession is a sign-in waiting for the code the user gets after opening URL.
type LoginSession interface {
	URL() string
	// Submit passes the code to the sign-in and waits for its outcome.
	Submit(code string) error
	Cancel()
}

// ModelProvider is implemented by providers whose runs can use a model chosen per chat.
type ModelProvider interface {
	// Models lists the model names or aliases a chat can switch to.
	Models() []string
}

// ModelClient is implemented by clients that can run a given model.
type ModelClient interface {
	UseModel(model string) Client
}

// EphemeralClient is implemented by clients that can run without persisting the
// session, so one-off runs leave no conversation behind.
type EphemeralClient interface {
	Ephemeral() Client
}

// ToolEvent describes a tool call the agent has started.
type ToolEvent struct {
	Name   string
	Detail string
}

// ToolHandler receives tool calls as they stream from the CLI.
type ToolHandler func(ToolEvent)

// ProgressClient is implemented by clients that report tool calls while running.
type ProgressClient interface {
	OnToolUse(handler ToolHandler) Client
}

// PartialHandler receives the text of the assistant message being generated, growing
// as the CLI streams it.
type PartialHandler func(text string)

// PartialClient is implemented by clients that stream partial assistant text.
type PartialClient interface {
	OnPartialText(handler PartialHandler) Client
}
