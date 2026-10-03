// Package procrun runs CLI binaries with captured output. Canceling the run's
// context stops the whole process tree, not just the direct child.
package procrun

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

const waitDelay = 3 * time.Second

// Cmd describes an external command and holds the output of its last run.
type Cmd struct {
	dest     string
	execPath string
	autoExe  bool
	args     []string
	env      []string
	dir      string
	stdIn    io.Reader
	stdOutW  io.Writer
	timeout  time.Duration
	stdOut   []byte
	stdErr   []byte
}

// New creates an empty Cmd.
func New() *Cmd {
	return &Cmd{}
}

// ExecPath sets the binary name or path; with AutoExe, ".exe" is appended on Windows.
func (c *Cmd) ExecPath(execPath string) *Cmd {
	if c.autoExe && runtime.GOOS == "windows" && execPath != "" && strings.ToLower(filepath.Ext(execPath)) != ".exe" {
		execPath += ".exe"
	}
	c.execPath = execPath
	return c
}

// AutoExe appends ".exe" to the binary name on Windows.
func (c *Cmd) AutoExe() *Cmd {
	c.autoExe = true
	return c.ExecPath(c.execPath)
}

// Dest sets the directory containing the binary; empty means a PATH lookup.
func (c *Cmd) Dest(dest string) *Cmd {
	c.dest = dest
	return c
}

// Arg appends an argument with optional values.
func (c *Cmd) Arg(name string, values ...string) *Cmd {
	c.args = append(c.args, name)
	c.args = append(c.args, values...)
	return c
}

// Args returns the arguments added with Arg.
func (c *Cmd) Args() []string {
	return c.args
}

// Path returns the path of the binary to execute.
func (c *Cmd) Path() string {
	if c.dest == "." {
		return c.dest + string(filepath.Separator) + c.execPath
	}
	return filepath.Join(c.dest, c.execPath)
}

// Dir sets the working directory.
func (c *Cmd) Dir(dir string) *Cmd {
	c.dir = dir
	return c
}

// Env sets the environment; nil inherits the current process environment.
func (c *Cmd) Env(env []string) *Cmd {
	c.env = env
	return c
}

// StdIn sets the reader the command reads stdin from.
func (c *Cmd) StdIn(r io.Reader) *Cmd {
	c.stdIn = r
	return c
}

// SetStdOut streams stdout to w instead of capturing it.
func (c *Cmd) SetStdOut(w io.Writer) *Cmd {
	c.stdOutW = w
	return c
}

// Timeout limits each run; zero means no limit.
func (c *Cmd) Timeout(d time.Duration) *Cmd {
	c.timeout = d
	return c
}

// Reset clears arguments, I/O settings, environment, working directory and captured output.
func (c *Cmd) Reset() *Cmd {
	c.args, c.env, c.dir = nil, nil, ""
	c.stdIn, c.stdOutW = nil, nil
	c.stdOut, c.stdErr = nil, nil
	return c
}

// StdOut returns the stdout captured by the last run.
func (c *Cmd) StdOut() []byte {
	return c.stdOut
}

// StdErr returns the stderr captured by the last run.
func (c *Cmd) StdErr() []byte {
	return c.stdErr
}

// CombinedOutput returns the captured stdout followed by stderr.
func (c *Cmd) CombinedOutput() []byte {
	out := make([]byte, 0, len(c.stdOut)+len(c.stdErr))
	out = append(out, c.stdOut...)
	return append(out, c.stdErr...)
}

// Run executes the command with the configured arguments followed by arg. When ctx is
// canceled or the timeout expires, the process group is terminated and the returned
// error wraps the context's cause.
func (c *Cmd) Run(ctx context.Context, arg ...string) error {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, c.Path(), append(slices.Clone(c.args), arg...)...)
	cmd.Dir, cmd.Env, cmd.Stdin = c.dir, c.env, c.stdIn
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if c.stdOutW != nil {
		cmd.Stdout = c.stdOutW
	}
	cmd.WaitDelay = waitDelay
	configure(cmd)

	err := cmd.Run()
	c.stdOut, c.stdErr = nil, stderr.Bytes()
	if c.stdOutW == nil {
		c.stdOut = stdout.Bytes()
	}
	return c.result(ctx, cmd, err)
}

func (c *Cmd) result(ctx context.Context, cmd *exec.Cmd, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		sweep(cmd)
		return fmt.Errorf("%w: %w", context.Cause(ctx), err)
	}
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
		return nil
	}
	return err
}
