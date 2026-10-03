//go:build unix

package procrun

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func shell(script string) *Cmd {
	return New().ExecPath("sh").Arg("-c", script)
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	var pid int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	return pid
}

func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func TestRun_CapturesOutput(t *testing.T) {
	c := shell(`echo out; echo err >&2`)
	require.NoError(t, c.Run(context.Background()))

	assert.Equal(t, "out\n", string(c.StdOut()))
	assert.Equal(t, "err\n", string(c.StdErr()))
	assert.Equal(t, "out\nerr\n", string(c.CombinedOutput()))
}

func TestRun_AppendsRunArgs(t *testing.T) {
	c := New().ExecPath("sh").Arg("-c", `echo "$0 $1"`)
	require.NoError(t, c.Run(context.Background(), "a", "b"))
	assert.Equal(t, "a b\n", string(c.StdOut()))
	assert.Equal(t, []string{"-c", `echo "$0 $1"`}, c.Args())
}

func TestRun_NonZeroExit(t *testing.T) {
	err := shell(`exit 3`).Run(context.Background())
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 3, exitErr.ExitCode())
}

func TestRun_StreamsStdout(t *testing.T) {
	var buf bytes.Buffer
	c := shell(`echo streamed`).SetStdOut(&buf)
	require.NoError(t, c.Run(context.Background()))
	assert.Equal(t, "streamed\n", buf.String())
	assert.Nil(t, c.StdOut())
}

func TestRun_WorkingDirAndEnv(t *testing.T) {
	dir := t.TempDir()
	c := shell(`pwd; echo "$FOO"`).Dir(dir).Env([]string{"FOO=bar"})
	require.NoError(t, c.Run(context.Background()))
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	assert.Equal(t, resolved+"\nbar\n", string(c.StdOut()))
}

func TestRun_CancelKillsProcessTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	cause := errors.New("stopped by user")
	ctx, cancel := context.WithCancelCause(context.Background())

	done := make(chan error, 1)
	go func() { done <- shell(`sleep 30 & echo $! > ` + pidFile + `; wait`).Run(ctx) }()
	pid := readPID(t, pidFile)
	cancel(cause)

	select {
	case err := <-done:
		assert.ErrorIs(t, err, cause)
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	assert.Eventually(t, func() bool { return !alive(pid) }, 2*time.Second, 10*time.Millisecond)
}

func TestRun_CancelKillsChildrenIgnoringSIGTERM(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- shell(`trap "" TERM; sleep 30 & echo $! > ` + pidFile + `; wait`).Run(ctx) }()
	pid := readPID(t, pidFile)
	cancel()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, context.Canceled)
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	assert.Eventually(t, func() bool { return !alive(pid) }, 2*time.Second, 10*time.Millisecond)
}

func TestRun_BackgroundPipeHolderDoesNotHang(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	start := time.Now()

	err := shell(`sleep 30 & echo $! > ` + pidFile + `; echo done`).Run(context.Background())

	require.NoError(t, err)
	assert.Less(t, time.Since(start), 10*time.Second)
	pid := readPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
}

func TestRun_Timeout(t *testing.T) {
	err := shell(`sleep 30`).Timeout(100 * time.Millisecond).Run(context.Background())
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestPathAndReset(t *testing.T) {
	c := New().ExecPath("claude").AutoExe()
	assert.Equal(t, "claude", c.Path())
	assert.Equal(t, "/opt/bin/claude", c.Dest("/opt/bin").Path())
	assert.Equal(t, "./claude", c.Dest(".").Path())

	c.Arg("-p").Dir("/tmp").Env([]string{"A=1"})
	c.Reset()
	assert.Empty(t, c.Args())
	assert.Empty(t, c.dir)
	assert.Nil(t, c.env)
}
