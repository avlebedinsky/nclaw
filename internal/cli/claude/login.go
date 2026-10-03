package claude

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nickalie/nclaw/internal/cli"
)

const (
	loginPrompt = "Paste code here if prompted >"
	loginKill   = 3 * time.Second
)

var (
	loginURLRe         = regexp.MustCompile(`(https://\S+)\s`)
	loginURLTimeout    = 30 * time.Second
	loginSubmitTimeout = time.Minute
)

// SetLoginEmail pre-fills the account email on the sign-in page started by StartLogin.
func (p *Provider) SetLoginEmail(email string) {
	p.loginEmail = email
}

// StartLogin runs "claude auth login" and returns once it has printed the sign-in link.
// The code from the page is then passed to Submit. Implements cli.LoginProvider.
func (p *Provider) StartLogin(ctx context.Context) (cli.LoginSession, error) {
	args := []string{"auth", "login", "--claudeai"}
	if p.loginEmail != "" {
		args = append(args, "--email", p.loginEmail)
	}
	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(runCtx, cmp.Or(p.execPath, "claude"), args...)
	cmd.WaitDelay = loginKill
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("claude: login stdin: %w", err)
	}
	out := &loginOutput{url: make(chan string, 1)}
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("claude: start login: %w", err)
	}

	s := &loginSession{stdin: stdin, out: out, cancel: cancel, done: make(chan struct{})}
	go func() {
		s.err = cmd.Wait()
		close(s.done)
	}()
	return s.awaitURL()
}

type loginSession struct {
	stdin  io.WriteCloser
	out    *loginOutput
	cancel context.CancelFunc
	done   chan struct{}
	err    error
	url    string
}

func (s *loginSession) awaitURL() (cli.LoginSession, error) {
	select {
	case s.url = <-s.out.url:
		return s, nil
	case <-s.done:
		s.cancel()
		return nil, fmt.Errorf("claude: login exited without a link: %s", s.out.lastLine())
	case <-time.After(loginURLTimeout):
		s.Cancel()
		return nil, errors.New("claude: login printed no link in time")
	}
}

func (s *loginSession) URL() string {
	return s.url
}

func (s *loginSession) Submit(code string) error {
	if _, err := io.WriteString(s.stdin, strings.TrimSpace(code)+"\n"); err != nil {
		s.Cancel()
		return fmt.Errorf("claude: send login code: %w", err)
	}
	select {
	case <-s.done:
	case <-time.After(loginSubmitTimeout):
		s.Cancel()
		return errors.New("claude: login did not finish in time")
	}
	if s.err != nil {
		return fmt.Errorf("claude: %s", s.out.lastLine())
	}
	return nil
}

func (s *loginSession) Cancel() {
	s.cancel()
	<-s.done
}

type loginOutput struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	url  chan string
	sent bool
}

func (o *loginOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.buf.Write(p)
	if !o.sent {
		if m := loginURLRe.FindSubmatch(o.buf.Bytes()); m != nil {
			o.sent = true
			o.url <- string(m[1])
		}
	}
	return len(p), nil
}

func (o *loginOutput) lastLine() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(o.buf.String()), "\n")
	last := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[len(lines)-1]), loginPrompt))
	return cmp.Or(last, "no output")
}
