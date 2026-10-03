package handler

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/nickalie/nclaw/internal/chatqueue"
	"github.com/nickalie/nclaw/internal/cli"
)

var loginWindow = 10 * time.Minute

// Logins runs interactive sign-ins of the CLI backend started with /login from the admin chat.
type Logins struct {
	Provider    cli.LoginProvider
	AdminChatID int64

	mu      sync.Mutex
	pending map[int64]*pendingLogin
}

type pendingLogin struct {
	session cli.LoginSession
	cancel  context.CancelFunc
}

func (l *Logins) put(chatID int64, p *pendingLogin) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pending == nil {
		l.pending = make(map[int64]*pendingLogin)
	}
	if prev := l.pending[chatID]; prev != nil {
		prev.cancel()
	}
	l.pending[chatID] = p
}

func (l *Logins) take(chatID int64, want *pendingLogin) *pendingLogin {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.pending[chatID]
	if p == nil || (want != nil && p != want) {
		return nil
	}
	delete(l.pending, chatID)
	return p
}

func (h *Handler) login(key chatqueue.Key) {
	if key.ChatID != h.Logins.AdminChatID {
		go h.notify(key, "/login works only in the admin's private chat with the bot.")
		return
	}
	go h.startLogin(key)
}

func (h *Handler) startLogin(key chatqueue.Key) {
	ctx, cancel := context.WithTimeout(context.Background(), loginWindow)
	session, err := h.Logins.Provider.StartLogin(ctx)
	if err != nil {
		cancel()
		log.Printf("handler: start login: %v", err)
		h.notify(key, "Could not start the sign-in: "+err.Error())
		return
	}
	p := &pendingLogin{session: session, cancel: cancel}
	h.Logins.put(key.ChatID, p)
	context.AfterFunc(ctx, func() {
		if h.Logins.take(key.ChatID, p) != nil {
			h.notify(key, "⌛ The sign-in was not finished in time. Send /login to start again.")
		}
	})
	h.notify(key, "🔑 Open this link and sign in with the bot's account:\n"+session.URL()+
		"\n\nThen send me the code shown at the end as your next message. The link works for 10 minutes.")
}

func (h *Handler) takeLoginCode(msg *models.Message) bool {
	if h.Logins == nil || msg.Text == "" || strings.HasPrefix(msg.Text, "/") {
		return false
	}
	p := h.Logins.take(msg.Chat.ID, nil)
	if p == nil {
		return false
	}
	key := chatqueue.Key{ChatID: msg.Chat.ID, ThreadID: msg.MessageThreadID}
	log.Printf("handler: sign-in code received in chat=%d", key.ChatID)
	go h.finishLogin(key, p, msg.Text)
	return true
}

func (h *Handler) finishLogin(key chatqueue.Key, p *pendingLogin, code string) {
	defer p.cancel()
	if err := p.session.Submit(code); err != nil {
		log.Printf("handler: sign-in failed: %v", err)
		h.notify(key, "❌ Sign-in failed: "+err.Error()+"\nSend /login to try again.")
		return
	}
	log.Printf("handler: signed in from chat=%d", key.ChatID)
	text := "✅ Signed in."
	if h.AuthStatus != nil {
		if line := h.AuthStatus(); line != "" {
			text += "\n" + line
		}
	}
	h.notify(key, text)
}
