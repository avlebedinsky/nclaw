package handler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nickalie/nclaw/internal/cli"
)

type fakeLoginSession struct {
	mu        sync.Mutex
	codes     []string
	submitErr error
	canceled  bool
}

func (s *fakeLoginSession) URL() string { return "https://claude.example/authorize" }

func (s *fakeLoginSession) Submit(code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes = append(s.codes, code)
	return s.submitErr
}

func (s *fakeLoginSession) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.canceled = true
}

func (s *fakeLoginSession) submitted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.codes...)
}

type fakeLoginProvider struct {
	session  *fakeLoginSession
	startErr error
	ctx      context.Context
}

func (p *fakeLoginProvider) StartLogin(ctx context.Context) (cli.LoginSession, error) {
	p.ctx = ctx
	if p.startErr != nil {
		return nil, p.startErr
	}
	return p.session, nil
}

func newLoginHandler(t *testing.T, lp *fakeLoginProvider) (*Handler, *safeSent) {
	t.Helper()
	sent := &safeSent{}
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, sent.send)
	h.Send = sent.send
	h.Logins = &Logins{Provider: lp, AdminChatID: 100}
	return h, sent
}

func waitSent(t *testing.T, sent *safeSent, n int) []string {
	t.Helper()
	require.Eventually(t, func() bool { return len(sent.all()) >= n }, 5*time.Second, 10*time.Millisecond)
	return sent.all()
}

func chatMessage(text string) *models.Update {
	return &models.Update{Message: &models.Message{ID: 7, Text: text, Chat: models.Chat{ID: 100}}}
}

func TestLogin_SubmitsNextMessageAsCode(t *testing.T) {
	lp := &fakeLoginProvider{session: &fakeLoginSession{}}
	h, sent := newLoginHandler(t, lp)
	h.AuthStatus = func() string { return "Claude sign-in: valid until Sun 1 Nov 18:00 MSK (29 days left)." }
	require.True(t, h.MatchCommand(commandUpdate("/login")))

	h.Command(context.Background(), nil, commandUpdate("/login"))
	msgs := waitSent(t, sent, 1)
	assert.Contains(t, msgs[0], "https://claude.example/authorize")

	h.Default(context.Background(), nil, chatMessage("abc#def"))
	msgs = waitSent(t, sent, 2)
	assert.Equal(t, "✅ Вход выполнен.\nClaude sign-in: valid until Sun 1 Nov 18:00 MSK (29 days left).", msgs[1])
	assert.Equal(t, []string{"abc#def"}, lp.session.submitted())
	assert.Equal(t, 0, h.Queue.Snapshot(testKey).PendingUser)
	require.Eventually(t, func() bool { return lp.ctx.Err() != nil }, time.Second, 10*time.Millisecond)

	h.Default(context.Background(), nil, chatMessage("hello again"))
	assert.Len(t, lp.session.submitted(), 1)
}

func TestLogin_ReportsRejectedCode(t *testing.T) {
	lp := &fakeLoginProvider{session: &fakeLoginSession{submitErr: errors.New("claude: Login failed: Request failed with status code 400")}}
	h, sent := newLoginHandler(t, lp)

	h.Command(context.Background(), nil, commandUpdate("/login"))
	waitSent(t, sent, 1)
	h.Default(context.Background(), nil, chatMessage("bad"))

	msgs := waitSent(t, sent, 2)
	assert.Equal(t, "❌ Войти не удалось: claude: Login failed: Request failed with status code 400\nОтправьте /login, чтобы попробовать ещё раз.", msgs[1])
}

func TestLogin_OnlyInAdminChat(t *testing.T) {
	lp := &fakeLoginProvider{session: &fakeLoginSession{}}
	h, sent := newLoginHandler(t, lp)
	h.Logins.AdminChatID = 555

	h.Command(context.Background(), nil, commandUpdate("/login"))

	msgs := waitSent(t, sent, 1)
	assert.Equal(t, "/login работает только в личном чате администратора с ботом.", msgs[0])
	assert.Nil(t, lp.ctx)
}

func TestLogin_StartFailure(t *testing.T) {
	lp := &fakeLoginProvider{startErr: errors.New("claude: login printed no link in time")}
	h, sent := newLoginHandler(t, lp)

	h.Command(context.Background(), nil, commandUpdate("/login"))

	msgs := waitSent(t, sent, 1)
	assert.Equal(t, "Не удалось начать вход: claude: login printed no link in time", msgs[0])
	h.Default(context.Background(), nil, chatMessage("abc#def"))
	assert.Equal(t, 1, h.Queue.Snapshot(testKey).PendingUser)
}

func TestLogin_ExpiresAfterWindow(t *testing.T) {
	orig := loginWindow
	loginWindow = 50 * time.Millisecond
	t.Cleanup(func() { loginWindow = orig })
	lp := &fakeLoginProvider{session: &fakeLoginSession{}}
	h, sent := newLoginHandler(t, lp)

	h.Command(context.Background(), nil, commandUpdate("/login"))

	msgs := waitSent(t, sent, 2)
	assert.Contains(t, msgs[1], "не завершён вовремя")
	h.Default(context.Background(), nil, chatMessage("abc#def"))
	assert.Empty(t, lp.session.submitted())
}

func TestLogin_CommandsAreNotTakenAsCode(t *testing.T) {
	lp := &fakeLoginProvider{session: &fakeLoginSession{}}
	h, sent := newLoginHandler(t, lp)

	h.Command(context.Background(), nil, commandUpdate("/login"))
	waitSent(t, sent, 1)
	h.Default(context.Background(), nil, chatMessage("/help"))

	assert.Empty(t, lp.session.submitted())
}

func TestLogin_DisabledWithoutLogins(t *testing.T) {
	h := newTestHandler(t, &mockProvider{client: &mockClient{}}, nil)

	assert.False(t, h.MatchCommand(commandUpdate("/login")))
}
